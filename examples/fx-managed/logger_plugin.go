package main

import (
	"net/http"

	"connectrpc.com/connect"
	connectplugin "github.com/masegraye/connect-plugin-go"
	loggercap "github.com/masegraye/connect-plugin-go/examples/capabilities/logger"
	"github.com/masegraye/connect-plugin-go/gen/capability/logger/v1/loggerv1connect"
)

// LoggerPlugin wraps the logger capability as a plugin for in-memory use.
type LoggerPlugin struct{}

func (p *LoggerPlugin) Metadata() connectplugin.PluginMetadata {
	// Use the path with leading slash (matches what ConnectServer returns)
	path := "/" + loggerv1connect.LoggerName + "/"
	return connectplugin.PluginMetadata{
		Name:    "logger",
		Path:    path,
		Version: "1.0.0",
		Provides: []connectplugin.ServiceDeclaration{
			{
				Type:    "logger",
				Version: "1.0.0",
				Path:    path,
			},
		},
	}
}

func (p *LoggerPlugin) ConnectServer(impl any) (string, http.Handler, error) {
	logger, ok := impl.(*loggercap.LoggerCapability)
	if !ok {
		logger = loggercap.NewLoggerCapability()
	}

	path, handler := loggerv1connect.NewLoggerHandler(logger)
	return path, handler, nil
}

func (p *LoggerPlugin) ConnectClient(baseURL string, httpClient connect.HTTPClient) (any, error) {
	return loggerv1connect.NewLoggerClient(httpClient, baseURL), nil
}
