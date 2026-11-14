package config

import (
	"errors"
	"fmt"
	"strings"

	yamlv4 "go.yaml.in/yaml/v4"
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
	var document yamlv4.Node
	err := yamlv4.Load(content, &document)
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

func mappingNode(parent *yamlv4.Node, key string) (*yamlv4.Node, error) {
	val, err := lookupMappingKey(parent, key)
	if err != nil {
		return nil, err
	}
	if val.Kind == yamlv4.AliasNode {
		return nil, errors.New("config pin must not use a shared YAML alias")
	}
	if val.Kind != yamlv4.MappingNode {
		return nil, fmt.Errorf("config value %q must be a mapping", key)
	}
	return val, nil
}

func pinScalarNode(parent *yamlv4.Node, key string) (*yamlv4.Node, error) {
	node, err := lookupMappingKey(parent, key)
	if err != nil {
		return nil, err
	}
	if node.Kind == yamlv4.AliasNode {
		return nil, errors.New("config pin must not use a shared YAML alias")
	}
	if node.Anchor != "" {
		return nil, errors.New("config pin must not define a YAML anchor; remove the anchor before updating")
	}
	if node.Kind != yamlv4.ScalarNode || node.Style == yamlv4.LiteralStyle || node.Style == yamlv4.FoldedStyle {
		return nil, errors.New("config pin must be a plain or quoted scalar")
	}
	return node, nil
}

func lookupMappingKey(node *yamlv4.Node, key string) (*yamlv4.Node, error) {
	if node == nil || node.Kind != yamlv4.MappingNode {
		return nil, fmt.Errorf("config value %q must be a mapping", key)
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1], nil
		}
	}
	return nil, fmt.Errorf("config is missing key %q", key)
}

func replaceScalar(lines []string, node *yamlv4.Node, newValue string) error {
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
	case yamlv4.SingleQuotedStyle:
		formatted = "'" + strings.ReplaceAll(newValue, "'", "''") + "'"
	case yamlv4.DoubleQuotedStyle:
		formatted = `"` + newValue + `"`
	}
	lines[node.Line-1] = line[:start] + formatted + line[end:]
	return nil
}

func scalarEnd(line string, start int, style yamlv4.Style) int {
	if style == yamlv4.SingleQuotedStyle || style == yamlv4.DoubleQuotedStyle {
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
