//go:build !nospell

// SPDX-License-Identifier: LGPL-3.0-only AND MIT
// Package-local Hunspell binding, adapted from sthorne/go-hunspell (MIT),
// derived from akhenakh/hunspellgo (LGPL-3.0, upstream commit dfad81264f74).
// Both notices and the GPL/LGPL texts are in licenses/bindings/.
// Modified in TTYloom: retain only used calls; fix suggestion-list handling,
// handle lifetime and locking (2026-09-05). The derived binding is LGPL-3.0;
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
// #cgo darwin LDFLAGS: -lhunspell-1.3 -L/usr/local/Cellar/hunspell/1.3.2/lib
// #cgo darwin CFLAGS: -I/usr/local/Cellar/hunspell/1.3.2/include
// #cgo freebsd CFLAGS: -I/usr/local/include
// #cgo freebsd LDFLAGS: -L/usr/local/lib -lhunspell-1.3
//
// #include <stdlib.h>
// #include <stdio.h>
// #include <hunspell/hunspell.h>
import "C"

import (
	"runtime"
	"sync"
	"unsafe"
)

// hunhandle wraps one C Hunhandle. The lock serialises the C calls: a handle
// is not thread-safe.
type hunhandle struct {
	handle *C.Hunhandle
	lock   *sync.Mutex
}

// newHunspell opens the aff/dic pair.
func newHunspell(affpath string, dpath string) *hunhandle {
	affpathcs := C.CString(affpath)
	defer C.free(unsafe.Pointer(affpathcs))

	dpathcs := C.CString(dpath)
	defer C.free(unsafe.Pointer(dpathcs))

	h := &hunhandle{lock: new(sync.Mutex)}
	h.handle = C.Hunspell_create(affpathcs, dpathcs)

	runtime.SetFinalizer(h, func(handle *hunhandle) {
		C.Hunspell_destroy(handle.handle)
		handle.handle = nil
	})

	return h
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
	wordcs := C.CString(word)
	defer C.free(unsafe.Pointer(wordcs))

	var carray **C.char
	var length C.int
	handle.lock.Lock()
	defer handle.lock.Unlock()
	length = C.Hunspell_suggest(handle.handle, &carray, wordcs)

	words := cArrayToString(carray, int(length))

	C.Hunspell_free_list(handle.handle, &carray, length)
	runtime.KeepAlive(handle)
	return words
}

// Add puts word in the in-memory dictionary of the handle.
func (handle *hunhandle) Add(word string) {
	cWord := C.CString(word)
	defer C.free(unsafe.Pointer(cWord))

	handle.lock.Lock()
	defer handle.lock.Unlock()
	C.Hunspell_add(handle.handle, cWord)
	runtime.KeepAlive(handle)
}

// Spell tells whether word is in the dictionary.
func (handle *hunhandle) Spell(word string) bool {
	wordcs := C.CString(word)
	defer C.free(unsafe.Pointer(wordcs))
	handle.lock.Lock()
	res := C.Hunspell_spell(handle.handle, wordcs)
	handle.lock.Unlock()
	runtime.KeepAlive(handle)

	return int(res) != 0
}
