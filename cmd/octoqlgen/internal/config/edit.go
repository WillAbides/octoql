// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package config

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	yamlv3 "gopkg.in/yaml.v3"
)

// UpdatePin preserves the original YAML while replacing only schema checksum
// and an applicable remote revision.
func UpdatePin(content []byte, sha256, revision string) ([]byte, error) {
	var document yamlv3.Node
	err := yamlv3.Unmarshal(content, &document)
	if err != nil {
		return nil, fmt.Errorf("decoding config for update: %w", err)
	}
	if len(document.Content) != 1 {
		return nil, errors.New("config must contain one YAML document")
	}
	root := dereference(document.Content[0])
	schemaNode, err := mappingValue(root, "schema")
	if err != nil {
		return nil, err
	}
	schemaNode = dereference(schemaNode)
	shaNode, err := mappingValue(schemaNode, "sha256")
	if err != nil {
		return nil, err
	}
	replacements := []scalarReplacement{{node: shaNode, value: sha256}}
	if revision != "" {
		sourceNode, sourceErr := mappingValue(schemaNode, "source")
		if sourceErr != nil {
			return nil, sourceErr
		}
		sourceNode = dereference(sourceNode)
		for _, key := range []string{"github_docs", "github_repository"} {
			var variant *yamlv3.Node
			variant, sourceErr = mappingValue(sourceNode, key)
			if sourceErr == nil {
				var revisionNode *yamlv3.Node
				revisionNode, sourceErr = mappingValue(dereference(variant), "revision")
				if sourceErr != nil {
					return nil, sourceErr
				}
				replacements = append(replacements, scalarReplacement{node: revisionNode, value: revision})
				break
			}
		}
	}
	return replaceScalars(content, replacements)
}

type scalarReplacement struct {
	node  *yamlv3.Node
	value string
}

func replaceScalars(content []byte, replacements []scalarReplacement) ([]byte, error) {
	lines := bytes.SplitAfter(content, []byte("\n"))
	for _, replacement := range replacements {
		if replacement.node.Kind == yamlv3.AliasNode {
			return nil, errors.New("config pin must not use a shared YAML alias")
		}
		node := dereference(replacement.node)
		if node.Kind != yamlv3.ScalarNode || node.Line < 1 || node.Line > len(lines) {
			return nil, errors.New("config pin must be a scalar value")
		}
		line := lines[node.Line-1]
		start := node.Column - 1
		if start < 0 || start >= len(line) {
			return nil, errors.New("config pin has an invalid scalar position")
		}
		end := scalarEnd(line, start, node.Style)
		if end <= start {
			return nil, errors.New("config pin has an unsupported scalar style")
		}
		value := formatScalar(replacement.value, node.Style)
		lines[node.Line-1] = append(append(append([]byte{}, line[:start]...), value...), line[end:]...)
	}
	return bytes.Join(lines, nil), nil
}

func scalarEnd(line []byte, start int, style yamlv3.Style) int {
	if style == yamlv3.SingleQuotedStyle || style == yamlv3.DoubleQuotedStyle {
		quote := line[start]
		for index := start + 1; index < len(line); index++ {
			if line[index] == quote && line[index-1] != '\\' {
				return index + 1
			}
		}
		return 0
	}
	end := start
	for end < len(line) && line[end] != '#' && line[end] != '\n' && line[end] != '\r' && line[end] != ' ' && line[end] != '\t' {
		end++
	}
	return end
}

func formatScalar(value string, style yamlv3.Style) []byte {
	if style == yamlv3.SingleQuotedStyle {
		return []byte("'" + strings.ReplaceAll(value, "'", "''") + "'")
	}
	if style == yamlv3.DoubleQuotedStyle {
		return []byte(`"` + value + `"`)
	}
	return []byte(value)
}

func dereference(node *yamlv3.Node) *yamlv3.Node {
	for node != nil && node.Kind == yamlv3.AliasNode {
		node = node.Alias
	}
	return node
}

func mappingValue(node *yamlv3.Node, key string) (*yamlv3.Node, error) {
	if node == nil || node.Kind != yamlv3.MappingNode {
		return nil, fmt.Errorf("config value %q must be a mapping", key)
	}
	var value *yamlv3.Node
	for index := 0; index < len(node.Content); index += 2 {
		if node.Content[index].Value != key {
			continue
		}
		if value != nil {
			return nil, fmt.Errorf("config contains duplicate key %q", key)
		}
		value = node.Content[index+1]
	}
	if value != nil {
		return value, nil
	}
	for index := 0; index < len(node.Content); index += 2 {
		if node.Content[index].Value != "<<" {
			continue
		}
		merged := dereference(node.Content[index+1])
		if merged.Kind == yamlv3.SequenceNode {
			for _, entry := range merged.Content {
				candidate, candidateErr := mappingValue(dereference(entry), key)
				if candidateErr != nil {
					continue
				}
				if value != nil {
					return nil, fmt.Errorf("config has conflicting merged key %q", key)
				}
				value = candidate
			}
			continue
		}
		candidate, candidateErr := mappingValue(merged, key)
		if candidateErr != nil {
			continue
		}
		if value != nil {
			return nil, fmt.Errorf("config has conflicting merged key %q", key)
		}
		value = candidate
	}
	if value != nil {
		return value, nil
	}
	return nil, fmt.Errorf("config is missing key %q", key)
}
