package config

import (
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v4"
)

const schemaDirectivePrefix = "# yaml-language-server: $schema="

// SetSchemaDirective adds or replaces the yaml-language-server schema directive.
func SetSchemaDirective(content []byte, schemaURL string) []byte {
	directive := schemaDirectivePrefix + schemaURL
	text := string(content)
	lines := strings.SplitAfter(text, "\n")
	for index, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), schemaDirectivePrefix) {
			continue
		}
		lineEnding := line[len(strings.TrimRight(line, "\r\n")):]
		lines[index] = directive + lineEnding
		return []byte(strings.Join(lines, ""))
	}

	lineEnding := "\n"
	newline := strings.IndexByte(text, '\n')
	if newline > 0 && text[newline-1] == '\r' {
		lineEnding = "\r\n"
	}
	return []byte(directive + lineEnding + lineEnding + text)
}

// UpdatePin preserves the original YAML while replacing only schema checksum
// and an applicable remote revision.
func UpdatePin(content []byte, sha256, revision string) ([]byte, error) {
	var document yaml.Node
	err := yaml.Load(content, &document)
	if err != nil {
		return nil, fmt.Errorf("decoding config for update: %w", err)
	}
	if len(document.Content) != 1 {
		return nil, errors.New("config must contain one YAML document")
	}

	schemaNode, err := mappingNode(document.Content[0], "schema")
	if err != nil {
		return nil, err
	}
	shaNode, err := pinScalarNode(schemaNode, "sha256")
	if err != nil {
		return nil, err
	}

	lines := strings.SplitAfter(string(content), "\n")
	err = replaceScalar(lines, shaNode, sha256)
	if err != nil {
		return nil, err
	}

	if revision != "" {
		sourceNode, sourceErr := mappingNode(schemaNode, "source")
		if sourceErr != nil {
			return nil, sourceErr
		}
		revisionNode, revErr := pinScalarNode(sourceNode, "revision")
		if revErr != nil {
			return nil, revErr
		}
		err = replaceScalar(lines, revisionNode, revision)
		if err != nil {
			return nil, err
		}
	}

	return []byte(strings.Join(lines, "")), nil
}

var errMissingMappingKey = errors.New("config is missing key")

func mappingNode(parent *yaml.Node, key string) (*yaml.Node, error) {
	val, err := lookupMappingKey(parent, key)
	if err != nil {
		return nil, err
	}
	if val.Kind == yaml.AliasNode {
		return nil, errors.New("config pin must not use a shared YAML alias")
	}
	if val.Anchor != "" {
		return nil, errors.New("config pin must not define a YAML anchor; remove the anchor before updating")
	}
	if val.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config value %q must be a mapping", key)
	}
	return val, nil
}

func pinScalarNode(parent *yaml.Node, key string) (*yaml.Node, error) {
	node, err := lookupMappingKey(parent, key)
	if err != nil {
		return nil, err
	}
	if node.Kind == yaml.AliasNode {
		return nil, errors.New("config pin must not use a shared YAML alias")
	}
	if node.Anchor != "" {
		return nil, errors.New("config pin must not define a YAML anchor; remove the anchor before updating")
	}
	if node.Kind != yaml.ScalarNode || node.Style == yaml.LiteralStyle || node.Style == yaml.FoldedStyle {
		return nil, errors.New("config pin must be a plain or quoted scalar")
	}
	return node, nil
}

func lookupMappingKey(node *yaml.Node, key string) (*yaml.Node, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config value %q must be a mapping", key)
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if !isMergeKey(node.Content[index]) && node.Content[index].Value == key {
			return node.Content[index+1], nil
		}
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if !isMergeKey(node.Content[index]) {
			continue
		}
		candidates := []*yaml.Node{node.Content[index+1]}
		if node.Content[index+1].Kind == yaml.SequenceNode {
			candidates = node.Content[index+1].Content
		}
		for _, candidate := range candidates {
			if candidate.Kind == yaml.AliasNode {
				_, aliasErr := lookupMappingKey(candidate.Alias, key)
				if aliasErr == nil {
					return nil, errors.New("config pin must not use a shared YAML alias")
				}
				if !errors.Is(aliasErr, errMissingMappingKey) {
					return nil, aliasErr
				}
				continue
			}
			found, err := lookupMappingKey(candidate, key)
			if errors.Is(err, errMissingMappingKey) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if candidate.Anchor != "" {
				return nil, errors.New("config pin must not define a YAML anchor; remove the anchor before updating")
			}
			return found, nil
		}
	}
	return nil, fmt.Errorf("%w %q", errMissingMappingKey, key)
}

func isMergeKey(node *yaml.Node) bool {
	if node.Kind != yaml.ScalarNode || node.Value != "<<" {
		return false
	}
	return node.Tag == "!!merge" || (node.Tag == "" && node.Style == 0)
}

func replaceScalar(lines []string, node *yaml.Node, newValue string) error {
	if node.Line < 1 || node.Line > len(lines) {
		return errors.New("config pin must be a scalar value")
	}
	line := lines[node.Line-1]
	start := node.Column - 1
	if start < 0 || start >= len(line) {
		return errors.New("config pin has an invalid scalar position")
	}
	end := scalarEnd(line, start, node.Style)
	if end <= start {
		return errors.New("config pin has an unsupported scalar style")
	}

	formatted := newValue
	switch node.Style {
	case yaml.SingleQuotedStyle:
		formatted = "'" + strings.ReplaceAll(newValue, "'", "''") + "'"
	case yaml.DoubleQuotedStyle:
		formatted = `"` + newValue + `"`
	}
	lines[node.Line-1] = line[:start] + formatted + line[end:]
	return nil
}

func scalarEnd(line string, start int, style yaml.Style) int {
	if style == yaml.SingleQuotedStyle || style == yaml.DoubleQuotedStyle {
		quote := line[start]
		for index := start + 1; index < len(line); index++ {
			if line[index] == quote && line[index-1] != '\\' {
				return index + 1
			}
		}
		return 0
	}
	end := start
	for end < len(line) && !strings.ContainsRune("#\n\r \t,}]", rune(line[end])) {
		end++
	}
	return end
}
