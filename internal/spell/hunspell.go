//go:build !nospell

// SPDX-License-Identifier: LGPL-3.0-only AND MIT
// Package-local Hunspell binding, adapted from sthorne/go-hunspell (MIT),
// derived from akhenakh/hunspellgo (LGPL-3.0, upstream commit dfad81264f74).
// Both notices and the GPL/LGPL texts are in licenses/bindings/.
// Modified in TTYloom: retain only used calls; fix suggestion-list handling,
// handle lifetime and locking (2026-09-05); convert the words to the encoding
// of the dictionary (2026-11-19). The derived binding is LGPL-3.0;
// Sean Thorne's notice is retained below for his MIT contributions.
//
// Copyright (c) 2014 Sean Thorne
//
// Permission is hereby granted, free of charge, to any person obtaining
// a copy of this software and associated documentation files (the
// "Software"), to deal in the Software without restriction, including
// without limitation the rights to use, copy, modify, merge, publish,
// distribute, sublicense, and/or sell copies of the Software, and to
// permit persons to whom the Software is furnished to do so, subject to
// the following conditions:
//
// The above copyright notice and this permission notice shall be
// included in all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
// EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
// MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
// NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE
// LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION
// OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION
// WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

package spell

// #cgo linux LDFLAGS: -lhunspell
//
// #include <stdlib.h>
// #include <stdio.h>
// #include <hunspell/hunspell.h>
import "C"

import (
	"errors"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"

	"github.com/govlog/ttyloom/internal/i18n"
)

// hunhandle wraps one C Hunhandle. The lock serialises the C calls: a handle
// is not thread-safe.
type hunhandle struct {
	handle *C.Hunhandle
	lock   *sync.Mutex
	enc    encoding.Encoding // encoding of the dictionary, nil for UTF-8
}

// dicEncodings : the encodings of a dictionary (SET of its .aff, ISO8859-1
// without one) that are not UTF-8, by encKey. The C API takes and gives words
// in that encoding.
var dicEncodings = map[string]encoding.Encoding{
	"iso88591": charmap.ISO8859_1, "iso88592": charmap.ISO8859_2, "iso88593": charmap.ISO8859_3,
	"iso88594": charmap.ISO8859_4, "iso88595": charmap.ISO8859_5, "iso88596": charmap.ISO8859_6,
	"iso88597": charmap.ISO8859_7, "iso88598": charmap.ISO8859_8, "iso88599": charmap.ISO8859_9,
	"iso885910": charmap.ISO8859_10, "iso885913": charmap.ISO8859_13, "iso885914": charmap.ISO8859_14,
	"iso885915": charmap.ISO8859_15, "iso885916": charmap.ISO8859_16,
	"koi8r": charmap.KOI8R, "koi8u": charmap.KOI8U,
	"cp1251": charmap.Windows1251, "microsoftcp1251": charmap.Windows1251,
	// Windows-874 adds characters to TIS-620 in 0x80-0x9F only; a TIS-620
	// dictionary holds none of them, so these words stay wrong.
	"tis620": charmap.Windows874, "tis6202533": charmap.Windows874, "iso885911": charmap.Windows874,
}

// encKey : the name of an encoding as Hunspell compares it, lower case
// letters and digits only ("ISO8859-1" and "iso-8859-1" give "iso88591").
func encKey(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 'a' - 'A'
		}
		return -1
	}, s)
}

// newHunspell opens the aff/dic pair. A dictionary in an encoding with no
// converter here is refused.
func newHunspell(affpath string, dpath string) (*hunhandle, error) {
	affpathcs := C.CString(affpath)
	defer C.free(unsafe.Pointer(affpathcs))

	dpathcs := C.CString(dpath)
	defer C.free(unsafe.Pointer(dpathcs))

	h := &hunhandle{lock: new(sync.Mutex)}
	h.handle = C.Hunspell_create(affpathcs, dpathcs)

	// The string belongs to the handle: no free.
	if set := C.GoString(C.Hunspell_get_dic_encoding(h.handle)); encKey(set) != "utf8" {
		if h.enc = dicEncodings[encKey(set)]; h.enc == nil {
			C.Hunspell_destroy(h.handle)
			return nil, errors.New(i18n.T("spell_dict_encoding", affpath, set))
		}
	}

	runtime.SetFinalizer(h, func(handle *hunhandle) {
		C.Hunspell_destroy(handle.handle)
		handle.handle = nil
	})

	return h, nil
}

// toDic gives word in the encoding of the dictionary; false when that
// encoding cannot hold it.
func (handle *hunhandle) toDic(word string) (string, bool) {
	if handle.enc == nil {
		return word, true
	}
	s, err := handle.enc.NewEncoder().String(word)
	return s, err == nil
}

// fromDic gives s, a word in the encoding of the dictionary, in UTF-8.
func (handle *hunhandle) fromDic(s string) string {
	if handle.enc == nil {
		return s
	}
	u, _ := handle.enc.NewDecoder().String(s) // a charmap decodes every byte
	return u
}

// cArrayToString copies a C array of l C strings into Go strings. Upstream
// built the slice with reflect.SliceHeader, which go vet now refuses;
// unsafe.Slice is the same operation.
func cArrayToString(c **C.char, l int) []string {
	s := []string{}
	for _, v := range unsafe.Slice(c, l) {
		s = append(s, C.GoString(v))
	}
	return s
}

// Suggest returns hunspell's suggestions for word.
func (handle *hunhandle) Suggest(word string) []string {
	w, ok := handle.toDic(word)
	if !ok {
		return nil
	}
	wordcs := C.CString(w)
	defer C.free(unsafe.Pointer(wordcs))

	var carray **C.char
	var length C.int
	handle.lock.Lock()
	defer handle.lock.Unlock()
	length = C.Hunspell_suggest(handle.handle, &carray, wordcs)

	words := cArrayToString(carray, int(length))

	C.Hunspell_free_list(handle.handle, &carray, length)
	runtime.KeepAlive(handle)
	for i, s := range words {
		words[i] = handle.fromDic(s)
	}
	return words
}

// Add puts word in the in-memory dictionary of the handle. A word that the
// encoding of the dictionary cannot hold is not added (Checker.cache keeps it
// right).
func (handle *hunhandle) Add(word string) {
	w, ok := handle.toDic(word)
	if !ok {
		return
	}
	cWord := C.CString(w)
	defer C.free(unsafe.Pointer(cWord))

	handle.lock.Lock()
	defer handle.lock.Unlock()
	C.Hunspell_add(handle.handle, cWord)
	runtime.KeepAlive(handle)
}

// Spell tells whether word is in the dictionary. A word that its encoding
// cannot hold is not.
func (handle *hunhandle) Spell(word string) bool {
	w, ok := handle.toDic(word)
	if !ok {
		return false
	}
	wordcs := C.CString(w)
	defer C.free(unsafe.Pointer(wordcs))
	handle.lock.Lock()
	res := C.Hunspell_spell(handle.handle, wordcs)
	handle.lock.Unlock()
	runtime.KeepAlive(handle)

	return int(res) != 0
}
