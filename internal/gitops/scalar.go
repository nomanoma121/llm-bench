package gitops

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

func GetScalar(doc []byte, path []string) (string, error) {
	_, node, err := scalar(doc, path)
	if err != nil {
		return "", err
	}
	return node.Value, nil
}

func SetScalar(doc []byte, path []string, value string) ([]byte, error) {
	root, node, err := scalar(doc, path)
	if err != nil {
		return nil, err
	}
	node.Value = value
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	return out.Bytes(), enc.Close()
}

func scalar(doc []byte, path []string) (*yaml.Node, *yaml.Node, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(doc, &root); err != nil {
		return nil, nil, err
	}
	if len(root.Content) == 0 {
		return nil, nil, fmt.Errorf("gitops: empty document")
	}
	node := root.Content[0]
	for _, key := range path {
		var next *yaml.Node
		if node.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(node.Content); i += 2 {
				if node.Content[i].Value == key {
					next = node.Content[i+1]
				}
			}
		}
		if next == nil {
			return nil, nil, fmt.Errorf("gitops: %s not found", strings.Join(path, "."))
		}
		node = next
	}
	if node.Kind != yaml.ScalarNode {
		return nil, nil, fmt.Errorf("gitops: %s is not a scalar", strings.Join(path, "."))
	}
	return &root, node, nil
}
