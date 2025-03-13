package yaml2json

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func yaml2json(y interface{}) (interface{}, error) {
	// Any value which has type interface{} needs to be converted by
	// recursively calling yaml2json.
	switch y := y.(type) {
	case map[interface{}]interface{}:
		j := make(map[string]interface{})
		for k, v := range y {
			kstr, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("map key is not a string: %T", k)
			}
			vjson, err := yaml2json(v)
			if err != nil {
				return nil, err
			}
			j[kstr] = vjson
		}
		return j, nil
	case []interface{}:
		j := make([]interface{}, len(y))
		for i, v := range y {
			vjson, err := yaml2json(v)
			if err != nil {
				return nil, err
			}
			j[i] = vjson
		}
		return j, nil
	}

	// The YAML type is also a valid JSON type
	return y, nil
}

// YAML2JSON assumes y can only be a "standard" yaml.v2 type: []interface{},
// map[interface{}]interface{}, string, bool, etc.  No custom types allowed.
func YAML2JSON(y interface{}) (interface{}, error) {
	return yaml2json(y)
}

// JSONFromYAMLFile reads a supplied YAML file, converts it to JSON with
// YAML2JSON, then returns the JSON encoded byte interpretation
func JSONFromYAMLFile(path string) ([]byte, error) {
	path = filepath.Clean(path)
	yamlCfg, err := os.ReadFile(path) //#nosec G304
	if err != nil {
		return nil, err
	}
	cfg := interface{}(nil)
	err = yaml.Unmarshal(yamlCfg, &cfg)
	if err != nil {
		return nil, err
	}
	cfg, err = YAML2JSON(cfg)
	if err != nil {
		return nil, err
	}
	cfgBytes, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return cfgBytes, nil
}
