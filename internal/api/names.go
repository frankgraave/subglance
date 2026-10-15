package api

import (
	"fmt"
	"unicode/utf8"

	"github.com/frankgraave/subglance/internal/store"
)

// maxNameLen is the longest name a monitor, a channel or an API token takes.
// It is counted in characters, as every other name limit in SubGlance is,
// so a name in a script with multibyte letters gets the same room as one in
// ASCII. A monitor's name is the subject of every alert about it, and
// Discord refuses an embed title over 256 characters: without a ceiling, one
// long name turned every alert for that monitor into a failed delivery.
const maxNameLen = 100

// nameProblem refuses a name that holds a control character (see
// store.HasControlCharacter) or is longer than maxNameLen characters. A
// blank name is the caller's to refuse: a monitor rename and a create word
// that refusal differently.
func nameProblem(name string) problem {
	if store.HasControlCharacter(name) {
		return fieldProblem("name", "name "+store.NoControlCharacters)
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		return fieldProblem("name", fmt.Sprintf("name must be %d characters or fewer", maxNameLen))
	}
	return problem{}
}
