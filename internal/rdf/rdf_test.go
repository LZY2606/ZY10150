package rdf

import "testing"

func TestTurtleBasics(t *testing.T) {
	src := []byte(`
@prefix ex: <http://ex.org/> .
@prefix xsd: <http://www.w3.org/2001/XMLSchema#> .
ex:a a ex:Thing ; ex:name "Alice"@en ; ex:age 30 ; ex:home ex:a, ex:b .
ex:b ex:addr [ ex:city "Tokyo" ; ex:zip "100" ] .
ex:c ex:nums ( 1 2 ) .
`)
	quads, pre, err := ParseTurtle(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pre["ex"] != "http://ex.org/" {
		t.Fatalf("prefixes: %v", pre)
	}
	if len(quads) == 0 {
		t.Fatal("no quads")
	}
	v := NewView(quads)
	if got := v.Objects("http://ex.org/a", "http://ex.org/age"); len(got) != 1 ||
		got[0].Datatype != XSDInteger || got[0].Value != "30" {
		t.Fatalf("age literal wrong: %+v", got)
	}
	if got := v.Objects("http://ex.org/a", "http://ex.org/name"); len(got) != 1 ||
		got[0].Lang != "en" {
		t.Fatalf("lang literal wrong: %+v", got)
	}
	if len(v.Objects("http://ex.org/a", "http://ex.org/home")) != 2 {
		t.Fatal("multi object")
	}
	// blank node property list
	addr := v.Objects("http://ex.org/b", "http://ex.org/addr")
	if len(addr) != 1 || !addr[0].IsBlank() {
		t.Fatalf("addr blank: %+v", addr)
	}
	if got := v.Objects(addr[0].Value, "http://ex.org/city"); len(got) != 1 || got[0].Value != "Tokyo" {
		t.Fatalf("nested blank props: %+v", got)
	}
	// rdf list
	nums := v.Objects("http://ex.org/c", "http://ex.org/nums")
	if len(nums) != 1 || !nums[0].IsBlank() {
		t.Fatalf("list head: %+v", nums)
	}
}

func TestCanonicalBlankNodeStability(t *testing.T) {
	// Two isomorphic graphs with different parser bnode ids and statement order.
	src1 := []byte(`
@prefix ex: <http://ex.org/> .
ex:p ex:knows _:z1 .
_:z1 ex:name "Zoe" ; ex:age 40 ; ex:knows _:z2 .
_:z2 ex:name "Bob" ; ex:age 41 .
`)
	src2 := []byte(`
@prefix ex: <http://ex.org/> .
_:aaa ex:name "Bob" ; ex:age 41 .
_:bbb ex:name "Zoe" ; ex:age 40 ; ex:knows _:aaa .
ex:p ex:knows _:bbb .
`)
	q1, _, err := ParseTurtle(src1)
	if err != nil {
		t.Fatal(err)
	}
	q2, _, err := ParseTurtle(src2)
	if err != nil {
		t.Fatal(err)
	}
	c1 := CanonicalizeDataset(NewDataset(q1...))
	c2 := CanonicalizeDataset(NewDataset(q2...))
	if c1.Fingerprint != c2.Fingerprint {
		t.Fatalf("expected equal fingerprints:\n%s\nvs\n%s", c1.Lines, c2.Lines)
	}
}

func TestCanonicalDistinguishes(t *testing.T) {
	src1 := []byte(`@prefix ex: <http://ex.org/> . _:x ex:name "A" ; ex:age 1 .`)
	src2 := []byte(`@prefix ex: <http://ex.org/> . _:x ex:name "A" ; ex:age 2 .`)
	q1, _, _ := ParseTurtle(src1)
	q2, _, _ := ParseTurtle(src2)
	if CanonicalizeDataset(NewDataset(q1...)).Fingerprint ==
		CanonicalizeDataset(NewDataset(q2...)).Fingerprint {
		t.Fatal("different content must hash differently")
	}
}

func TestJSONLDBasics(t *testing.T) {
	src := []byte(`{
	  "@context": {"name": "http://ex.org/name", "v1": "http://v1.org/",
	    "age": {"@id": "http://ex.org/age"}, "@vocab": "http://ex.org/"},
	  "@id": "v1:alice", "@type": "v1:Person",
	  "name": "Alice", "age": 30,
	  "addr": {"city": "Tokyo", "geo": {"@value": "35.6", "@type": "http://www.w3.org/2001/XMLSchema#double"}},
	  "nick": [{"@value": "Ally", "@language": "en"}]
	}`)
	quads, err := ParseJSONLD(src, "http://g/ex")
	if err != nil {
		t.Fatal(err)
	}
	v := NewView(quads)
	ty := v.Types("http://v1.org/alice")
	if len(ty) != 1 || ty[0].Value != "http://v1.org/Person" {
		t.Fatalf("type: %+v", ty)
	}
	age := v.Objects("http://v1.org/alice", "http://ex.org/age")
	if len(age) != 1 || age[0].Datatype != XSDInteger {
		t.Fatalf("age: %+v", age)
	}
	if quads[0].Graph != "http://g/ex" {
		t.Fatal("graph provenance lost")
	}
	addr := v.Objects("http://v1.org/alice", "http://ex.org/addr")
	if len(addr) != 1 || !addr[0].IsBlank() {
		t.Fatalf("nested addr bnode: %+v", addr)
	}
	geo := v.Objects(addr[0].Value, "http://ex.org/geo")
	if len(geo) != 1 || geo[0].Datatype != XSDDouble {
		t.Fatalf("typed double: %+v", geo)
	}
	nick := v.Objects("http://v1.org/alice", "http://ex.org/nick")
	if len(nick) != 1 || nick[0].Lang != "en" {
		t.Fatalf("lang: %+v", nick)
	}
}
