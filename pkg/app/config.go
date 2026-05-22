package app

import (
	"os"

	"github.com/pkg/errors"
	"sigs.k8s.io/yaml"
)

func LoadConfig(file string, out any) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return errors.Wrap(err, "read config")
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return errors.Wrap(err, "parse config")
	}
	return nil
}

func MustLoadConfig(file string, out any) {
	if err := LoadConfig(file, out); err != nil {
		panic(err)
	}
}
