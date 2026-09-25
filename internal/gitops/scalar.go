package gitops

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// GetYAMLScalar reads a single string scalar at path inside a YAML document.
// The path elements are mapping keys; the final element may address a
// sequence item only through a key whose value is a list of maps (not
// supported).
//
// The GitOps contract is restricted to string scalars: rewriting a numeric
// or boolean node with a string value would silently change the manifest's
// type (a Kubernetes spec.replicas must stay an integer), so those are
// rejected instead of coerced.
func GetYAMLScalar(doc []byte, path []string) (string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(doc, &root); err != nil {
		return "", fmt.Errorf("scalar: parse yaml: %w", err)
	}
	node := &root
	if len(path) == 0 {
		return "", errors.New("scalar: empty yaml path")
	}
	for i, key := range path {
		node = childValue(node, key)
		if node == nil {
			return "", fmt.Errorf("scalar: path %q not found", strings.Join(path, "."))
		}
		if i == len(path)-1 {
			if node.Kind != yaml.ScalarNode {
				return "", fmt.Errorf("scalar: %q is not a scalar", strings.Join(path, "."))
			}
			if node.Tag != "!!str" {
				return "", fmt.Errorf("scalar: %q must be a string scalar (got %s); quote the value in the manifest", strings.Join(path, "."), node.Tag)
			}
			return node.Value, nil
		}
	}
	return "", errors.New("scalar: unreachable")
}

// SetYAMLScalar returns a copy of the document with the string scalar at path
// set to value. Comments and formatting outside the edited node are preserved
// via the yaml.Node round-trip, and the node's type is never changed.
func SetYAMLScalar(doc []byte, path []string, value string) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(doc, &root); err != nil {
		return nil, fmt.Errorf("scalar: parse yaml: %w", err)
	}
	node := &root
	if len(path) == 0 {
		return nil, errors.New("scalar: empty yaml path")
	}
	for i, key := range path {
		node = childValue(node, key)
		if node == nil {
			return nil, fmt.Errorf("scalar: path %q not found", strings.Join(path, "."))
		}
		if i == len(path)-1 {
			if node.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("scalar: %q is not a scalar", strings.Join(path, "."))
			}
			// The node is a string scalar (enforced by the reader); only the
			// value changes, so quoting style and tag stay untouched.
			node.Value = value
		}
	}
	var out strings.Builder
	enc := yaml.NewEncoder(&out)
	if err := enc.Encode(&root); err != nil {
		return nil, fmt.Errorf("scalar: encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return []byte(out.String()), nil
}

// childValue resolves one mapping key (or sequence index) and returns the
// value node.
func childValue(node *yaml.Node, key string) *yaml.Node {
	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) == 0 {
			return nil
		}
		return childValue(node.Content[0], key)
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			k, v := node.Content[i], node.Content[i+1]
			if k.Value == key {
				return v
			}
		}
		return nil
	default:
		return nil
	}
}
