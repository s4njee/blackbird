package rtorrent

import (
	"context"

	"blackbird/internal/scgi/xmlrpc"
)

// Rename sets the torrent's display name via d.name.set. Vanilla rTorrent
// does not ship this command (it comes from rTorrent-PS/pyrocore-style
// patches), so callers should gate the UI on SupportsMethod("d.name.set")
// and surface the fault if the daemon rejects it anyway.
func (c *Client) Rename(ctx context.Context, hash, name string) error {
	return c.call(ctx, "d.name.set", hash, name)
}

// SupportsMethod probes whether the daemon exposes an XML-RPC method. The
// probe itself is a built-in, so a missing target command surfaces as a
// boolean false rather than a fault.
func (c *Client) SupportsMethod(ctx context.Context, method string) (bool, error) {
	res, err := c.scgi.Call(ctx, "system.methodExist", []xmlrpc.Value{{
		Type: "string", Str: method,
	}})
	if err != nil {
		return false, err
	}
	if len(res) == 0 {
		return false, nil
	}
	return bval(res[0]), nil
}
