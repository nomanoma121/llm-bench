package gitops

import (
	"strings"
	"testing"
)

const deployment = `apiVersion: apps/v1
kind: Deployment
spec:
  replicas: 1 # production
  template:
    spec:
      containers:
        - args:
            - -m
            - /models/a.gguf

            - --port
            - "8080"
          env:
            - name: A
              value: "x"
            - name: B
              value: 'y'
`

func TestSetScalarChangesOnlyThatLine(t *testing.T) {
	for _, tc := range []struct {
		path []string
		want string
	}{
		{[]string{"spec", "replicas"}, "  replicas: 0 # production\n"},
	} {
		out, err := SetScalar([]byte(deployment), tc.path, "0")
		if err != nil {
			t.Fatal(err)
		}
		a, b := strings.Split(deployment, "\n"), strings.Split(string(out), "\n")
		if len(a) != len(b) {
			t.Fatalf("line count changed:\n%s", out)
		}
		changed := 0
		for i := range a {
			if a[i] != b[i] {
				changed++
				if b[i]+"\n" != tc.want {
					t.Fatalf("got %q", b[i])
				}
			}
		}
		if changed != 1 {
			t.Fatalf("%d lines changed:\n%s", changed, out)
		}
		if v, _ := GetScalar(out, tc.path); v != "0" {
			t.Fatalf("read back %q", v)
		}
	}
}

func TestSetScalarKeepsQuoting(t *testing.T) {
	doc := "a: \"1\"\nb: '1'\n"
	out, _ := SetScalar([]byte(doc), []string{"a"}, "0")
	out, _ = SetScalar(out, []string{"b"}, "0")
	if string(out) != "a: \"0\"\nb: '0'\n" {
		t.Fatalf("got %q", out)
	}
}
