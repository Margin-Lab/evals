package agentdef

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var mcpServerNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func ValidateAndNormalizeUnifiedSpec(spec UnifiedSpec) (UnifiedSpec, error) {
	spec.Model = strings.TrimSpace(spec.Model)
	if spec.Model == "" {
		return UnifiedSpec{}, fmt.Errorf("model is required")
	}

	spec.ReasoningLevel = strings.ToLower(strings.TrimSpace(spec.ReasoningLevel))
	if spec.ReasoningLevel == "" {
		return UnifiedSpec{}, fmt.Errorf("reasoning_level is required")
	}

	if spec.MCP == nil {
		return spec, nil
	}
	if len(spec.MCP.Servers) == 0 {
		return UnifiedSpec{}, fmt.Errorf("mcp.servers must contain at least one server when mcp is provided")
	}

	normalizedServers := make([]MCPServer, 0, len(spec.MCP.Servers))
	seenNames := make(map[string]struct{}, len(spec.MCP.Servers))
	for idx := range spec.MCP.Servers {
		normalized, err := normalizeMCPServer(spec.MCP.Servers[idx])
		if err != nil {
			return UnifiedSpec{}, fmt.Errorf("mcp.servers[%d]: %w", idx, err)
		}
		if _, exists := seenNames[normalized.Name]; exists {
			return UnifiedSpec{}, fmt.Errorf("mcp.servers[%d]: duplicate server name %q", idx, normalized.Name)
		}
		seenNames[normalized.Name] = struct{}{}
		normalizedServers = append(normalizedServers, normalized)
	}

	sort.Slice(normalizedServers, func(i, j int) bool {
		return normalizedServers[i].Name < normalizedServers[j].Name
	})
	spec.MCP.Servers = normalizedServers
	return spec, nil
}

func normalizeMCPServer(server MCPServer) (MCPServer, error) {
	server.Name = strings.TrimSpace(server.Name)
	if server.Name == "" {
		return MCPServer{}, fmt.Errorf("name is required")
	}
	if !mcpServerNamePattern.MatchString(server.Name) {
		return MCPServer{}, fmt.Errorf("name %q must match %s", server.Name, mcpServerNamePattern.String())
	}

	server.Transport = MCPTransport(strings.ToLower(strings.TrimSpace(string(server.Transport))))
	switch server.Transport {
	case MCPTransportSTDIO, MCPTransportSSE, MCPTransportHTTP:
	default:
		return MCPServer{}, fmt.Errorf("transport must be one of %q, %q, %q", MCPTransportSTDIO, MCPTransportSSE, MCPTransportHTTP)
	}

	if server.TimeoutMS != nil && *server.TimeoutMS <= 0 {
		return MCPServer{}, fmt.Errorf("timeout_ms must be > 0 when set")
	}

	switch server.Transport {
	case MCPTransportSTDIO:
		if err := normalizeSTDIOFields(&server); err != nil {
			return MCPServer{}, err
		}
	default:
		if err := normalizeRemoteFields(&server); err != nil {
			return MCPServer{}, err
		}
	}

	return server, nil
}

func normalizeSTDIOFields(server *MCPServer) error {
	if strings.TrimSpace(server.URL) != "" {
		return fmt.Errorf("url is not allowed for stdio transport")
	}
	if len(server.Headers) > 0 {
		return fmt.Errorf("headers are not allowed for stdio transport")
	}
	if server.OAuth != nil {
		return fmt.Errorf("oauth is not allowed for stdio transport")
	}

	if len(server.Command) == 0 {
		return fmt.Errorf("command is required for stdio transport")
	}
	trimmedCommand := make([]string, 0, len(server.Command))
	for idx, part := range server.Command {
		part = strings.TrimSpace(part)
		if part == "" {
			return fmt.Errorf("command[%d] cannot be empty", idx)
		}
		trimmedCommand = append(trimmedCommand, part)
	}
	server.Command = trimmedCommand

	if len(server.Env) == 0 {
		server.Env = nil
		return nil
	}
	normalizedEnv := make(map[string]string, len(server.Env))
	for key, value := range server.Env {
		normalizedKey := strings.TrimSpace(key)
		if normalizedKey == "" {
			return fmt.Errorf("env keys cannot be empty")
		}
		if strings.Contains(normalizedKey, "=") {
			return fmt.Errorf("env key %q cannot contain '='", normalizedKey)
		}
		normalizedEnv[normalizedKey] = value
	}
	server.Env = normalizedEnv
	return nil
}

func normalizeRemoteFields(server *MCPServer) error {
	if len(server.Command) > 0 {
		return fmt.Errorf("command is only allowed for stdio transport")
	}
	if len(server.Env) > 0 {
		return fmt.Errorf("env is only allowed for stdio transport")
	}

	server.URL = strings.TrimSpace(server.URL)
	if server.URL == "" {
		return fmt.Errorf("url is required for %q transport", server.Transport)
	}
	parsedURL, err := url.Parse(server.URL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	scheme := strings.ToLower(strings.TrimSpace(parsedURL.Scheme))
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("url must use http or https scheme")
	}
	server.URL = parsedURL.String()

	if len(server.Headers) == 0 {
		server.Headers = nil
	} else {
		normalizedHeaders := make(map[string]string, len(server.Headers))
		for key, value := range server.Headers {
			normalizedKey := strings.TrimSpace(key)
			if normalizedKey == "" {
				return fmt.Errorf("headers keys cannot be empty")
			}
			normalizedHeaders[normalizedKey] = value
		}
		server.Headers = normalizedHeaders
	}

	if server.OAuth == nil {
		return nil
	}
	server.OAuth.ClientID = strings.TrimSpace(server.OAuth.ClientID)
	server.OAuth.ClientSecret = strings.TrimSpace(server.OAuth.ClientSecret)
	server.OAuth.Scope = strings.TrimSpace(server.OAuth.Scope)
	if server.OAuth.Disabled && (server.OAuth.ClientID != "" || server.OAuth.ClientSecret != "" || server.OAuth.Scope != "") {
		return fmt.Errorf("oauth.disabled cannot be combined with oauth.client_id/oauth.client_secret/oauth.scope")
	}
	return nil
}
