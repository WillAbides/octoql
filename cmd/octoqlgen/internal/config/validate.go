// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	canonicalSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	commitPattern          = regexp.MustCompile(`^[0-9a-f]{40}$`)
	githubVersionPattern   = regexp.MustCompile(`^(fpt|ghec|ghes-\d+\.\d+)$`)
	repositoryPartPattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	dnsLabelPattern        = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
)

func (s *Schema) SHA256Value() string {
	if s.Sha256 == nil {
		return ""
	}
	return *s.Sha256
}

func (s *Schema) SourceValue() Source {
	if s.Source == nil {
		return Source{}
	}
	return *s.Source
}

func ValidateSource(source Source, sha256 string) error {
	sourceCount := 0
	if source.GithubDocs != nil {
		sourceCount++
	}
	if source.GithubRepository != nil {
		sourceCount++
	}
	if source.Url != nil {
		sourceCount++
	}
	if sourceCount > 1 {
		return errors.New("schema.source must set exactly one remote source variant")
	}
	isRemote := sourceCount == 1
	if isRemote && sha256 == "" {
		return errors.New("schema.sha256 is required for remote sources")
	}
	if sha256 != "" && !canonicalSHA256Pattern.MatchString(sha256) {
		return errors.New("schema.sha256 must be a canonical 64-character lowercase hexadecimal sha-256")
	}

	if source.GithubDocs != nil {
		err := source.GithubDocs.Validate()
		if err != nil {
			return fmt.Errorf("schema.source.github_docs: %w", err)
		}
	}
	if source.GithubRepository != nil {
		err := source.GithubRepository.Validate()
		if err != nil {
			return fmt.Errorf("schema.source.github_repository: %w", err)
		}
	}
	if source.Url != nil {
		err := validateURL(*source.Url)
		if err != nil {
			return fmt.Errorf("schema.source.url: %w", err)
		}
	}
	return nil
}

func (g GitHubDocs) Validate() error {
	if !githubVersionPattern.MatchString(g.Version) {
		return errors.New("version must be fpt, ghec, or ghes-X.Y")
	}
	if !commitPattern.MatchString(g.Revision) {
		return errors.New("revision must be a full lowercase hexadecimal commit sha")
	}
	return nil
}

func (g *GitHubRepository) Validate() error {
	parts := strings.Split(g.Repository, "/")
	validParts := len(parts) == 2
	if validParts {
		validParts = validRepositoryPart(parts[0]) && validRepositoryPart(parts[1])
	}
	if !validParts {
		return errors.New("repository must be an owner/name pair")
	}
	if !commitPattern.MatchString(g.Revision) {
		return errors.New("revision must be a full lowercase hexadecimal commit sha")
	}

	cleanPath := path.Clean(g.Path)
	isInvalidPath := g.Path == "" ||
		strings.HasPrefix(g.Path, "/") ||
		cleanPath == "." ||
		cleanPath == ".." ||
		strings.HasPrefix(cleanPath, "../") ||
		cleanPath != g.Path ||
		strings.Contains(g.Path, `\`)
	if isInvalidPath {
		return errors.New("path must be a repository-relative file path without traversal")
	}
	if containsSpaceOrControl(g.Path) {
		return errors.New("path must not contain whitespace or control characters")
	}

	if g.Host == nil {
		defaultHost := "github.com"
		g.Host = &defaultHost
	}
	err := validateHost(*g.Host)
	if err != nil {
		return fmt.Errorf("host: %w", err)
	}
	return nil
}

func validRepositoryPart(value string) bool {
	if value == "." || value == ".." {
		return false
	}
	return repositoryPartPattern.MatchString(value)
}

func validateURL(value string) error {
	if value == "" {
		return errors.New("must be a nonempty HTTP or HTTPS URL")
	}
	if containsSpaceOrControl(value) {
		return errors.New("must not contain whitespace or control characters")
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return errors.New("must be a valid HTTP or HTTPS URL")
	}
	validScheme := strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")
	if !validScheme {
		return errors.New("scheme must be http or https")
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return errors.New("host is required")
	}
	if parsed.User != nil {
		return errors.New("credentials in URLs are not allowed")
	}
	if parsed.Opaque != "" {
		return errors.New("must use hierarchical URL syntax")
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return errors.New("port must be nonempty")
	}
	port := parsed.Port()
	if port != "" {
		err = validatePort(port)
		if err != nil {
			return fmt.Errorf("port: %w", err)
		}
	}
	return nil
}

func validateHost(value string) error {
	if value == "" {
		return errors.New("must not be empty")
	}
	if containsSpaceOrControl(value) {
		return errors.New("must not contain whitespace or control characters")
	}
	if strings.ContainsAny(value, "/@?#") {
		return errors.New("must contain only a hostname and optional port")
	}

	hostname, port, isIPv6, err := splitHost(value)
	if err != nil {
		return err
	}
	if port != "" {
		err = validatePort(port)
		if err != nil {
			return fmt.Errorf("port: %w", err)
		}
	}
	if isIPv6 {
		if strings.Contains(hostname, ".") {
			return errors.New("must contain a bracketed hexadecimal IPv6 address")
		}
		ip := net.ParseIP(hostname)
		if ip == nil || !strings.Contains(hostname, ":") {
			return errors.New("must contain a valid bracketed IPv6 address")
		}
		return nil
	}

	ip := net.ParseIP(hostname)
	if ip != nil {
		if ip.To4() == nil {
			return errors.New("IPv6 addresses must be bracketed")
		}
		return nil
	}
	if onlyDigitsAndDots(hostname) {
		return errors.New("must contain a valid IPv4 address")
	}
	return validateDNSName(hostname)
}

func splitHost(value string) (hostname, port string, isIPv6 bool, err error) {
	if strings.HasPrefix(value, "[") {
		closing := strings.Index(value, "]")
		if closing < 0 {
			return "", "", false, errors.New("must contain a valid bracketed IPv6 address")
		}
		hostname = value[1:closing]
		remainder := value[closing+1:]
		if remainder == "" {
			return hostname, "", true, nil
		}
		if !strings.HasPrefix(remainder, ":") {
			return "", "", false, errors.New("must contain only a hostname and optional port")
		}
		port = strings.TrimPrefix(remainder, ":")
		if port == "" {
			return "", "", false, errors.New("port must be nonempty")
		}
		return hostname, port, true, nil
	}

	colonCount := strings.Count(value, ":")
	if colonCount > 1 {
		return "", "", false, errors.New("IPv6 addresses must be bracketed")
	}
	if colonCount == 0 {
		return value, "", false, nil
	}
	hostname, port, _ = strings.Cut(value, ":")
	if hostname == "" {
		return "", "", false, errors.New("hostname must be nonempty")
	}
	if port == "" {
		return "", "", false, errors.New("port must be nonempty")
	}
	return hostname, port, false, nil
}

func validatePort(value string) error {
	for _, char := range value {
		if char < '0' || char > '9' {
			return errors.New("must be numeric")
		}
	}
	number, err := strconv.ParseUint(value, 10, 16)
	if err != nil || number > 65535 {
		return errors.New("must be between 0 and 65535")
	}
	return nil
}

func validateDNSName(value string) error {
	name := strings.TrimSuffix(value, ".")
	if name == "" || len(value) > 253 {
		return errors.New("must contain a valid DNS name")
	}
	for label := range strings.SplitSeq(name, ".") {
		if !dnsLabelPattern.MatchString(label) {
			return errors.New("must contain a valid DNS name")
		}
	}
	return nil
}

func onlyDigitsAndDots(value string) bool {
	for _, char := range value {
		if char != '.' && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func containsSpaceOrControl(value string) bool {
	for _, char := range value {
		if unicode.IsSpace(char) || unicode.IsControl(char) {
			return true
		}
	}
	return false
}
