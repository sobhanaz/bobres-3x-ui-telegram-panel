package branding

import _ "embed"

// botAvatar is the BOBRES logo as a square JPEG (640x640, Telegram's
// minimum), the bot's profile photo until the operator uploads their own.
//
//go:embed assets/bot-avatar.jpg
var botAvatar []byte

// DefaultBotPhoto returns a copy of the default bot profile photo (a JPEG).
func DefaultBotPhoto() []byte { return append([]byte(nil), botAvatar...) }
