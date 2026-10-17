//go:build windows

package integration

import (
	"golang.org/x/sys/windows/registry"
)

const base = `Software\Classes\`

func keysFor(e Entry) []string {
	keys := []string{base + `*\shell\Homelydrive.` + e.ID}
	if e.Dirs {
		keys = append(keys, base+`Directory\shell\Homelydrive.`+e.ID)
	}
	return keys
}

// Install registers the entries for the current user (no admin rights needed).
func Install(exe, lang string) error {
	for _, e := range Entries(lang) {
		for _, k := range keysFor(e) {
			key, _, err := registry.CreateKey(registry.CURRENT_USER, k, registry.ALL_ACCESS)
			if err != nil {
				return err
			}
			key.SetStringValue("MUIVerb", e.Label)
			key.SetStringValue("Icon", exe+",0")
			key.SetStringValue("MultiSelectModel", "Player")
			key.Close()
			cmd, _, err := registry.CreateKey(registry.CURRENT_USER, k+`\command`, registry.ALL_ACCESS)
			if err != nil {
				return err
			}
			cmd.SetStringValue("", `"`+exe+`" `+e.Command+` "%1"`)
			cmd.Close()
		}
	}
	return nil
}

// Uninstall removes the entries.
func Uninstall() error {
	for _, e := range Entries("de") {
		for _, k := range keysFor(e) {
			registry.DeleteKey(registry.CURRENT_USER, k+`\command`)
			registry.DeleteKey(registry.CURRENT_USER, k)
		}
	}
	return nil
}
