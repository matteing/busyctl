package applemusic

import (
	"strings"
	"unicode/utf8"
)

var latinASCII = strings.NewReplacer(
	"À", "A", "Á", "A", "Â", "A", "Ã", "A", "Ä", "A", "Å", "A", "Æ", "AE",
	"à", "a", "á", "a", "â", "a", "ã", "a", "ä", "a", "å", "a", "æ", "ae",
	"Ç", "C", "ç", "c", "È", "E", "É", "E", "Ê", "E", "Ë", "E",
	"è", "e", "é", "e", "ê", "e", "ë", "e", "Ì", "I", "Í", "I", "Î", "I", "Ï", "I",
	"ì", "i", "í", "i", "î", "i", "ï", "i", "Ñ", "N", "ñ", "n",
	"Ò", "O", "Ó", "O", "Ô", "O", "Õ", "O", "Ö", "O", "Ø", "O", "Œ", "OE",
	"ò", "o", "ó", "o", "ô", "o", "õ", "o", "ö", "o", "ø", "o", "œ", "oe",
	"Š", "S", "š", "s", "ß", "ss", "Ù", "U", "Ú", "U", "Û", "U", "Ü", "U",
	"ù", "u", "ú", "u", "û", "u", "ü", "u", "Ý", "Y", "Ÿ", "Y", "ý", "y", "ÿ", "y",
	"Ž", "Z", "ž", "z", "‘", "'", "’", "'", "“", "\"", "”", "\"",
	"–", "-", "—", "-", "…", "...", "•", "-", "\u00a0", " ",
)

func cleanText(value string) string {
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "?")
	}
	value = latinASCII.Replace(value)
	var output strings.Builder
	unsupported := false
	for _, char := range value {
		if char >= 0x20 && char <= 0x7e {
			output.WriteRune(char)
			unsupported = false
		} else if !unsupported {
			output.WriteByte('?')
			unsupported = true
		}
	}
	return strings.Join(strings.Fields(output.String()), " ")
}
