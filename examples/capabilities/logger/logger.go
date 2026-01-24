package loggercap

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"connectrpc.com/connect"
	connectplugin "github.com/example/connect-plugin-go"
	loggerv1 "github.com/example/connect-plugin-go/gen/capability/logger/v1"
	"github.com/example/connect-plugin-go/gen/capability/logger/v1/loggerv1connect"
)

// LoggerCapability implements a simple logging capability for the host.
type LoggerCapability struct{}

var _ connectplugin.CapabilityHandler = (*LoggerCapability)(nil)

// NewLoggerCapability creates a new logger capability.
func NewLoggerCapability() *LoggerCapability {
	return &LoggerCapability{}
}

// CapabilityType returns the capability type.
func (l *LoggerCapability) CapabilityType() string {
	return "logger"
}

// Version returns the capability version.
func (l *LoggerCapability) Version() string {
	return "1.0.0"
}

// ServeHTTP implements http.Handler.
func (l *LoggerCapability) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Create Connect handler
	_, handler := loggerv1connect.NewLoggerHandler(l)
	handler.ServeHTTP(w, r)
}

// Log implements the Logger RPC.
func (l *LoggerCapability) Log(
	ctx context.Context,
	req *connect.Request[loggerv1.LogRequest],
) (*connect.Response[loggerv1.LogResponse], error) {
	// Simple logging to stdout
	msg := fmt.Sprintf("[%s] %s", req.Msg.Level, req.Msg.Message)

	// Add fields if present
	if len(req.Msg.Fields) > 0 {
		msg += " "
		for k, v := range req.Msg.Fields {
			msg += fmt.Sprintf("%s=%s ", k, v)
		}
	}

	log.Println(msg)

	return connect.NewResponse(&loggerv1.LogResponse{}), nil
}
