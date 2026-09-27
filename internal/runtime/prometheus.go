package runtime

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// promSample is one Prometheus text sample.
type promSample struct {
	Name   string
	Labels map[string]string
	Value  float64
}

// parsePrometheus reads the text exposition format. It accepts the subset a
// runtime exposes (counters, gauges and histograms summarized by the runtime)
// and ignores comments, `_bucket`/`_sum`/`_count` suffixes of histograms keep
// their names, which is intentional: renaming them would make the recorded
// metric names drift from the runtime's own documentation.
func parsePrometheus(r io.Reader) ([]promSample, error) {
	var samples []promSample
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, labels, rest, err := splitPromLine(line)
		if err != nil {
			return nil, err
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return nil, fmt.Errorf("runtime: prometheus line %q has no value", line)
		}
		v, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			return nil, fmt.Errorf("runtime: prometheus value %q: %w", fields[0], err)
		}
		samples = append(samples, promSample{Name: name, Labels: labels, Value: v})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("runtime: prometheus: %w", err)
	}
	return samples, nil
}

// splitPromLine splits `name{labels} value` into its parts.
func splitPromLine(line string) (name string, labels map[string]string, rest string, err error) {
	open := strings.IndexByte(line, '{')
	if open < 0 {
		fields := strings.SplitN(line, " ", 2)
		if len(fields) != 2 {
			return "", nil, "", fmt.Errorf("runtime: prometheus line %q", line)
		}
		return fields[0], nil, fields[1], nil
	}
	closeIdx := strings.IndexByte(line[open:], '}')
	if closeIdx < 0 {
		return "", nil, "", fmt.Errorf("runtime: prometheus line %q has an unterminated label set", line)
	}
	closeIdx += open
	name = line[:open]
	labels, err = parseLabels(line[open+1 : closeIdx])
	if err != nil {
		return "", nil, "", fmt.Errorf("runtime: prometheus line %q: %w", line, err)
	}
	return name, labels, strings.TrimSpace(line[closeIdx+1:]), nil
}

// parseLabels reads `k="v",k2="v2"`. Prometheus escapes backslash, quote and
// newline inside a value.
func parseLabels(s string) (map[string]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	labels := map[string]string{}
	i := 0
	for i < len(s) {
		eq := strings.IndexByte(s[i:], '=')
		if eq < 0 {
			return nil, fmt.Errorf("label %q has no value", s[i:])
		}
		key := strings.TrimSpace(s[i : i+eq])
		i += eq + 1
		if i >= len(s) || s[i] != '"' {
			return nil, fmt.Errorf("label %q is not quoted", key)
		}
		i++
		var b strings.Builder
		for i < len(s) && s[i] != '"' {
			if s[i] == '\\' && i+1 < len(s) {
				i++
				switch s[i] {
				case 'n':
					b.WriteByte('\n')
				case '\\', '"':
					b.WriteByte(s[i])
				default:
					b.WriteByte(s[i])
				}
				i++
				continue
			}
			b.WriteByte(s[i])
			i++
		}
		if i >= len(s) {
			return nil, fmt.Errorf("label %q has an unterminated value", key)
		}
		i++ // closing quote
		labels[key] = b.String()
		for i < len(s) && s[i] == ',' {
			i++
		}
	}
	return labels, nil
}
