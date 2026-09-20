package fixtures

import (
	"github.com/example/gsbmig/internal/rdf"
)

// Graph URNs used as named-graph provenance for each fixture source.
const (
	GraphOntologyV1  = "urn:gsb:ontology-v1"
	GraphOntologyV2  = "urn:gsb:ontology-v2"
	GraphCandidates  = "urn:gsb:mapping-candidates"
	GraphInstancesV1 = "urn:gsb:instances-v1"
	GraphExistingV2  = "urn:gsb:instances-existing-v2"
	GraphShapesV2    = "urn:gsb:shapes-v2"
	GraphMigrated    = "urn:gsb:migrated-v2"
)

type Bundle struct {
	OntologyV1  []rdf.Quad
	OntologyV2  []rdf.Quad
	Candidates  []rdf.Quad
	InstancesV1 []rdf.Quad
	ExistingV2  []rdf.Quad
	ShapesV2    []rdf.Quad
}

func Load() (*Bundle, error) {
	b := &Bundle{}
	var err error
	if b.OntologyV1, _, err = rdf.ParseTurtleGraph(MustRead("ontology-v1.ttl"), GraphOntologyV1); err != nil {
		return nil, err
	}
	if b.OntologyV2, _, err = rdf.ParseTurtleGraph(MustRead("ontology-v2.ttl"), GraphOntologyV2); err != nil {
		return nil, err
	}
	if b.Candidates, _, err = rdf.ParseTurtleGraph(MustRead("mapping-candidates.ttl"), GraphCandidates); err != nil {
		return nil, err
	}
	if b.InstancesV1, err = rdf.ParseJSONLD(MustRead("instances-v1.jsonld"), GraphInstancesV1); err != nil {
		return nil, err
	}
	if b.ExistingV2, _, err = rdf.ParseTurtleGraph(MustRead("instances-existing-v2.ttl"), GraphExistingV2); err != nil {
		return nil, err
	}
	if b.ShapesV2, _, err = rdf.ParseTurtleGraph(MustRead("shapes-v2.ttl"), GraphShapesV2); err != nil {
		return nil, err
	}
	return b, nil
}
