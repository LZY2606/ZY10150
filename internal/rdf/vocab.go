package rdf

// Core RDF/RDFS/OWL/SHACL/XSD constants used across the project.
const (
	RDFNamespace   = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	RDFSNamespace  = "http://www.w3.org/2000/01/rdf-schema#"
	OWLNamespace   = "http://www.w3.org/2002/07/owl#"
	SHACLNamespace = "http://www.w3.org/ns/shacl#"

	RDFType  = RDFNamespace + "type"
	RDFFirst = RDFNamespace + "first"
	RDFRest  = RDFNamespace + "rest"
	RDFNil   = RDFNamespace + "nil"

	RDFSLabel      = RDFSNamespace + "label"
	RDFSComment    = RDFSNamespace + "comment"
	RDFSSubClassOf = RDFSNamespace + "subClassOf"
	RDFSDomain     = RDFSNamespace + "domain"
	RDFSRange      = RDFSNamespace + "range"

	OWLClass            = OWLNamespace + "Class"
	OWLObjectProperty   = OWLNamespace + "ObjectProperty"
	OWLDatatypeProperty = OWLNamespace + "DatatypeProperty"
	OWLNamedIndividual  = OWLNamespace + "NamedIndividual"
	OWLDisjointWith     = OWLNamespace + "disjointWith"
	OWLDeprecated       = OWLNamespace + "deprecated"
	OWLEquivalentClass  = OWLNamespace + "equivalentClass"

	SHNodeShape   = SHACLNamespace + "NodeShape"
	SHProperty    = SHACLNamespace + "property"
	SHPath        = SHACLNamespace + "path"
	SHTargetClass = SHACLNamespace + "targetClass"
	SHTargetNode  = SHACLNamespace + "targetNode"
	SHNodeKind    = SHACLNamespace + "nodeKind"
	SHDataType    = SHACLNamespace + "datatype"
	SHClass       = SHACLNamespace + "class"
	SHMinCount    = SHACLNamespace + "minCount"
	SHMaxCount    = SHACLNamespace + "maxCount"
	SHIn          = SHACLNamespace + "in"
	SHClosed      = SHACLNamespace + "closed"
	SHIRI         = SHACLNamespace + "IRI"
	SHBlankNode   = SHACLNamespace + "BlankNode"
	SHLiteral     = SHACLNamespace + "Literal"
	SHDisjoint    = SHACLNamespace + "disjoint"
)
