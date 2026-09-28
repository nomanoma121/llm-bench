package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type modelSource struct {
	Repo     string   `yaml:"repo"`
	Revision string   `yaml:"revision"`
	Include  []string `yaml:"include"`
}

func loadModels(path string) (map[string]modelSource, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	models := map[string]modelSource{}
	if err := yaml.Unmarshal(b, &models); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for name, m := range models {
		if m.Repo == "" || !filepath.IsLocal(name) {
			return nil, fmt.Errorf("%s: model %q needs a repo and a plain directory name", path, name)
		}
	}
	return models, nil
}

func (m modelSource) downloadArgs(dir string) []string {
	args := []string{"download", m.Repo, "--local-dir", dir}
	if m.Revision != "" {
		args = append(args, "--revision", m.Revision)
	}
	for _, pattern := range m.Include {
		args = append(args, "--include", pattern)
	}
	return args
}

func newModelsCmd() *cobra.Command {
	var file, dir string
	cmd := &cobra.Command{Use: "models", Short: "Manage the model weights listed in models.yaml"}
	cmd.PersistentFlags().StringVar(&file, "file", "models.yaml", "model definitions")
	cmd.PersistentFlags().StringVar(&dir, "dir", "/models", "directory the models are downloaded into")
	cmd.AddCommand(&cobra.Command{
		Use:   "download [name...]",
		Short: "Download models from Hugging Face with the hf CLI (all of them by default)",
		RunE: func(cmd *cobra.Command, names []string) error {
			models, err := loadModels(file)
			if err != nil {
				return err
			}
			if len(names) == 0 {
				for name := range models {
					names = append(names, name)
				}
				sort.Strings(names)
			}
			for _, name := range names {
				m, ok := models[name]
				if !ok {
					return fmt.Errorf("model %q is not defined in %s", name, file)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "downloading %s from %s\n", name, m.Repo)
				hf := exec.CommandContext(cmd.Context(), "hf", m.downloadArgs(filepath.Join(dir, name))...)
				hf.Stdout, hf.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
				if err := hf.Run(); err != nil {
					return fmt.Errorf("download %s: %w", name, err)
				}
			}
			return nil
		},
	})
	return cmd
}
