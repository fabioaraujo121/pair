// Package nvim drives the running Neovim instance from the pair binary.
//
// Primary transport is msgpack-rpc over the --listen socket via
// neovim/go-client, restricted to Dial + ExecLua (the rest of that client
// predates Neovim 0.10 and calls removed API). Every call runs under a
// deadline because the client has no per-call context. The fallback is
// `nvim --server <addr> --remote-expr "luaeval(...)"`, which evaluates an
// expression in any editor mode; --remote-send is never used because it
// types keystrokes into whatever mode the editor is in.
package nvim

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	gonvim "github.com/neovim/go-client/nvim"
)

// Client talks to one Neovim instance.
type Client struct {
	Addr      string        // unix socket path or host:port
	Timeout   time.Duration // per call
	Transport string        // "" (rpc, then cli fallback), "rpc" or "cli"
}

// New returns a client with the default 2 s deadline. PAIR_NVIM_TRANSPORT
// pins the transport.
func New(addr string) *Client {
	return &Client{Addr: addr, Timeout: 2 * time.Second, Transport: os.Getenv("PAIR_NVIM_TRANSPORT")}
}

// Shown is what pair.nvim reports back after a show().
type Shown struct {
	File  string `msgpack:"file" json:"file"`
	Start int    `msgpack:"start" json:"start"`
	End   int    `msgpack:"end" json:"end"`
}

// Show jumps to file (absolute path) at line start and highlights
// start..end.
func (c *Client) Show(ctx context.Context, file string, start, end int) (Shown, error) {
	var out Shown
	err := c.ExecLua(ctx, "return require('pair').show(...)", &out, file, start, end)
	return out, err
}

// Diff draws a change inside the file: marks is the JSON of the added line
// numbers and the removed blocks (see hook.Marks).
func (c *Client) Diff(ctx context.Context, file, marks string) error {
	return c.ExecLua(ctx, "return require('pair').diff(...)", nil, file, marks)
}

// Reload runs :checktime so buffers pick up files changed on disk.
func (c *Client) Reload(ctx context.Context) error {
	return c.ExecLua(ctx, "return require('pair').reload()", nil)
}

// Ping returns the Neovim version string, proving the instance is reachable
// and the plugin is loaded.
func (c *Client) Ping(ctx context.Context) (string, error) {
	var v string
	err := c.ExecLua(ctx, "local ok = pcall(require, 'pair'); return tostring(vim.version()) .. (ok and ' pair.nvim loaded' or ' pair.nvim MISSING')", &v)
	return v, err
}

// ExecLua runs a Lua chunk (which may `return` a value into result) with the
// given arguments available as `...`.
func (c *Client) ExecLua(ctx context.Context, code string, result any, args ...any) error {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	switch c.Transport {
	case "cli":
		return c.execCLI(ctx, code, result, args...)
	case "rpc":
		return c.execRPC(ctx, code, result, args...)
	}
	rpcErr := c.execRPC(ctx, code, result, args...)
	if rpcErr == nil || ctx.Err() != nil {
		return rpcErr
	}
	if cliErr := c.execCLI(ctx, code, result, args...); cliErr != nil {
		return fmt.Errorf("rpc: %v; cli fallback: %v", rpcErr, cliErr)
	}
	return nil
}

func (c *Client) execRPC(ctx context.Context, code string, result any, args ...any) error {
	done := make(chan error, 1)
	go func() {
		v, err := gonvim.Dial(c.Addr)
		if err != nil {
			done <- fmt.Errorf("dial %s: %w", c.Addr, err)
			return
		}
		defer v.Close()
		done <- v.ExecLua(code, result, args...)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("nvim rpc: %w", ctx.Err())
	}
}

// execCLI wraps the chunk in a function so luaeval (an expression evaluator)
// can run it: (function(...) <code> end)(unpack(_A)).
func (c *Client) execCLI(ctx context.Context, code string, result any, args ...any) error {
	nvimBin, err := exec.LookPath("nvim")
	if err != nil {
		return err
	}
	expr := "(function(...) " + code + " end)(unpack(_A))"
	vimExpr := "luaeval(" + vimString(expr) + ", " + vimList(args) + ")"
	cmd := exec.CommandContext(ctx, nvimBin, "--server", c.Addr, "--remote-expr", vimExpr)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nvim --remote-expr: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if s, ok := result.(*string); ok && s != nil {
		*s = strings.TrimSpace(string(out))
	}
	return nil
}

// vimString quotes s as a Vim single-quoted string literal.
func vimString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// vimList renders args as a Vim list literal of strings and integers.
func vimList(args []any) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		switch v := a.(type) {
		case string:
			esc := strings.ReplaceAll(v, `\`, `\\`)
			esc = strings.ReplaceAll(esc, `"`, `\"`)
			parts = append(parts, `"`+esc+`"`)
		case int, int64, int32:
			parts = append(parts, fmt.Sprint(v))
		default:
			parts = append(parts, `"`+fmt.Sprint(v)+`"`)
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
