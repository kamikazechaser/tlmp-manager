// Package desktop sends notifications and opens URLs through the session D-Bus.
package desktop

import (
	"context"
	"log/slog"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

type (
	Opts struct {
		AppName string
	}

	// Desktop shows one notification and replaces it in place.
	// Without a session bus every method does nothing.
	Desktop struct {
		appName       string
		dialed        bool
		conn          *dbus.Conn
		markup        bool
		id            uint32
		progressStart time.Time
		progressShown time.Time
	}
)

const (
	callTimeout   = 2 * time.Second
	progressDelay = time.Second
	progressEvery = time.Second

	notifyDest = "org.freedesktop.Notifications"
	notifyPath = "/org/freedesktop/Notifications"
	portalDest = "org.freedesktop.portal.Desktop"
	portalPath = "/org/freedesktop/portal/desktop"
)

var (
	markupEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
)

// New connects to the session bus on first use.
func New(o Opts) *Desktop {
	return &Desktop{appName: o.AppName}
}

func (d *Desktop) Notify(summary, body string) {
	d.notify(summary, body, map[string]dbus.Variant{"urgency": dbus.MakeVariant(byte(1))})
}

// Progress stays silent for short downloads; the body must carry the numbers,
// because many daemons ignore the "value" hint.
func (d *Desktop) Progress(summary, body string, percent int) {
	now := time.Now()
	if d.progressStart.IsZero() {
		d.progressStart = now
	}
	if now.Sub(d.progressStart) < progressDelay || now.Sub(d.progressShown) < progressEvery {
		return
	}
	d.progressShown = now
	d.notify(summary, body, map[string]dbus.Variant{
		"urgency":   dbus.MakeVariant(byte(1)),
		"value":     dbus.MakeVariant(int32(min(max(percent, 0), 100))),
		"transient": dbus.MakeVariant(true),
	})
}

func (d *Desktop) Dismiss() {
	if d.conn == nil || d.id == 0 {
		return
	}
	if err := d.call(notifyDest, notifyPath, notifyDest+".CloseNotification", d.id).Err; err != nil {
		slog.Debug("Closing notification", "err", err)
	}
	d.id = 0
}

// OpenURL uses the desktop portal, because Steam counts a browser that the game starts as the game.
func (d *Desktop) OpenURL(url string) error {
	if d.dial() {
		err := d.call(portalDest, portalPath, "org.freedesktop.portal.OpenURI.OpenURI", "", url, map[string]dbus.Variant{}).Err
		if err == nil {
			return nil
		}
		slog.Debug("Desktop portal could not open the URL; trying xdg-open", "err", err)
	}
	cmd := exec.Command("xdg-open", url)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// Close leaves the notification on screen.
func (d *Desktop) Close() {
	if d.conn != nil {
		_ = d.conn.Close()
		d.conn = nil
	}
}

// dial uses a private connection, because the keyring owns the shared one.
func (d *Desktop) dial() bool {
	if d.dialed {
		return d.conn != nil
	}
	d.dialed = true

	conn, err := dbus.SessionBusPrivateNoAutoStartup()
	if err == nil {
		if err = conn.Auth(nil); err == nil {
			err = conn.Hello()
		}
		if err != nil {
			_ = conn.Close()
		}
	}
	if err != nil {
		slog.Debug("No session bus; desktop notifications disabled", "err", err)
		return false
	}

	d.conn = conn
	var caps []string
	if err := d.call(notifyDest, notifyPath, notifyDest+".GetCapabilities").Store(&caps); err == nil {
		d.markup = slices.Contains(caps, "body-markup")
	}
	return true
}

func (d *Desktop) notify(summary, body string, hints map[string]dbus.Variant) {
	if !d.dial() {
		return
	}
	if d.markup {
		body = markupEscaper.Replace(body)
	}
	var id uint32
	err := d.call(notifyDest, notifyPath, notifyDest+".Notify", d.appName, d.id, "", summary, body, []string{}, hints, int32(-1)).Store(&id)
	if err != nil {
		slog.Debug("Showing notification", "err", err)
		return
	}
	d.id = id
}

func (d *Desktop) call(dest string, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return d.conn.Object(dest, path).CallWithContext(ctx, method, 0, args...)
}
