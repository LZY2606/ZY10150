// Package fixtures embeds the bundled Turtle/JSON-LD demo data so the service
// runs with no external triple store.
package fixtures

import (
	"embed"
)

//go:embed *.ttl *.jsonld
var FS embed.FS

func MustRead(name string) []byte {
	b, err := FS.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return b
}
