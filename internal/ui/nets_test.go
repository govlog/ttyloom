package ui

// Network keys of the tests: the core names no network, its tests do.
const (
	netTelegram = "telegram"
	netDiscord  = "discord"
)

func ircNet(name string) string { return "irc:" + name }
