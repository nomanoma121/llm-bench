package runtime

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func parsePrometheus(r io.Reader) ([]Metric, error) {
	var out []Metric
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, rest := line, ""
		var labels map[string]string
		if open := strings.IndexByte(line, '{'); open >= 0 {
			end := strings.LastIndexByte(line, '}')
			if end < open {
				return nil, fmt.Errorf("runtime: prometheus line %q", line)
			}
			name, labels, rest = line[:open], parseLabels(line[open+1:end]), line[end+1:]
		} else if i := strings.IndexByte(line, ' '); i >= 0 {
			name, rest = line[:i], line[i:]
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return nil, fmt.Errorf("runtime: prometheus line %q has no value", line)
		}
		v, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			return nil, fmt.Errorf("runtime: prometheus line %q: %w", line, err)
		}
		out = append(out, Metric{Name: name, Value: v, Labels: labels})
	}
	return out, scanner.Err()
}

func parseLabels(s string) map[string]string {
	labels := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		labels[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	return labels
}
