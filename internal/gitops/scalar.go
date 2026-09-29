package gitops

import (
	"bytes"
	"fmt"
	"strconv"
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
	_, node, err := scalar(doc, path)
	if err != nil {
		return nil, err
	}
	lines := bytes.SplitAfter(doc, []byte("\n"))
	line := string(lines[node.Line-1])
	start := node.Column - 1
	var end int
	switch node.Style {
	case yaml.DoubleQuotedStyle:
		end = start + 1 + strings.Index(line[start+1:], `"`) + 1
		value = strconv.Quote(value)
	case yaml.SingleQuotedStyle:
		end = start + 1 + strings.Index(line[start+1:], "'") + 1
		value = "'" + value + "'"
	case 0:
		rest := strings.TrimRight(line[start:], "\r\n")
		if i := strings.Index(rest, " #"); i >= 0 {
			rest = rest[:i]
		}
		end = start + len(strings.TrimRight(rest, " \t"))
	default:
		return nil, fmt.Errorf("gitops: %s must be a single-line scalar", strings.Join(path, "."))
	}
	lines[node.Line-1] = []byte(line[:start] + value + line[end:])
	return bytes.Join(lines, nil), nil
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
