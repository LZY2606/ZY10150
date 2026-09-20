package web

import "sort"

var fixtureAllow = map[string]bool{
	"ontology_old.ttl":   true,
	"ontology_new.ttl":   true,
	"mappings.ttl":       true,
	"instances.jsonld":   true,
	"instances_more.ttl": true,
	"shapes_new.ttl":     true,
}

func safeFixture(name string) bool { return fixtureAllow[name] }

// fixtureDirs are searched in order (cwd then ./fixtures).
func fixtureDirs() []string {
	return []string{"fixtures", "./fixtures", "../fixtures", "../../fixtures"}
}

func FixtureNames() []string {
	var out []string
	for k := range fixtureAllow {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
