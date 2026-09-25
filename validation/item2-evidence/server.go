package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const logPath = "/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item2/tool.log"

type SleepInput struct {
	Seconds int `json:"seconds" jsonschema:"number of seconds to sleep"`
}

func logLine(format string, a ...any) {
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s pid=%d %s\n", time.Now().Format("15:04:05.000"), os.Getpid(), fmt.Sprintf(format, a...))
}

func sleepTool(ctx context.Context, req *mcp.CallToolRequest, in SleepInput) (*mcp.CallToolResult, any, error) {
	logLine("start %d", in.Seconds)
	select {
	case <-time.After(time.Duration(in.Seconds) * time.Second):
		logLine("done")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("slept %ds", in.Seconds)}}}, nil, nil
	case <-ctx.Done():
		logLine("cancelled: %v", ctx.Err())
		return nil, nil, ctx.Err()
	}
}

func main() {
	logLine("server starting")
	server := mcp.NewServer(&mcp.Implementation{Name: "pair", Version: "0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "sleep", Description: "Sleep for N seconds and return"}, sleepTool)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		logLine("server exit: %v", err)
		log.Fatal(err)
	}
	logLine("server exit clean")
}
