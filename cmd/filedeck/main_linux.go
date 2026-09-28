// filedeck provides a local operator CLI and an authenticated HTTP API.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/pbuzdygan/filedeck/internal/core"
	"github.com/pbuzdygan/filedeck/internal/storage"
	"golang.org/x/sys/unix"
)

// ownSpace is the name of the space at -root, Filedeck's own storage.
const ownSpace = "files"

// discoverSpaces returns the own space plus every directory directly inside
// spacesDir, named after it (e.g. /spaces/nas -> "nas"). Symlinks and names
// that are not valid space names are rejected rather than skipped silently.
func discoverSpaces(root, spacesDir string) (map[string]string, error) {
	spaces := map[string]string{}
	if root != "" {
		spaces[ownSpace] = root
	}
	if spacesDir == "" {
		return spaces, nil
	}
	entries, err := os.ReadDir(spacesDir)
	if errors.Is(err, os.ErrNotExist) {
		return spaces, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s/%s: only directories (mounts) are allowed in the spaces directory", spacesDir, e.Name())
		}
		if !core.ValidSpaceName(e.Name()) || e.Name() == ownSpace {
			return nil, fmt.Errorf("%s/%s: space names must match [a-z0-9][a-z0-9._-]* and must not be %q", spacesDir, e.Name(), ownSpace)
		}
		spaces[e.Name()] = filepath.Join(spacesDir, e.Name())
	}
	return spaces, nil
}

func ensureDir(dir string, mode os.FileMode) error {
	err := os.Mkdir(dir, mode)
	if err == nil || errors.Is(err, os.ErrExist) {
		return nil
	}
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("cannot create %s as uid %d: %w (the parent directory must be writable by this user)", dir, os.Geteuid(), err)
	}
	return err
}

func parseMode(name, fallback string) (os.FileMode, error) {
	v := env(name, fallback)
	m, err := strconv.ParseUint(v, 8, 32)
	if err != nil || m > 0777 {
		return 0, fmt.Errorf("FILEDECK_%s: invalid octal mode %q", name, v)
	}
	return os.FileMode(m), nil
}

// env returns FILEDECK_<NAME> so containers can configure flags; flags win.
func env(name, fallback string) string {
	if v, ok := os.LookupEnv("FILEDECK_" + name); ok {
		return v
	}
	return fallback
}
func envBool(name string) (bool, error) {
	v := env(name, "false")
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("FILEDECK_%s: invalid boolean %q", name, v)
	}
	return b, nil
}

func run(args []string) (err error) {
	insecureDefault, err := envBool("INSECURE_LOCAL")
	if err != nil {
		return err
	}
	selfSignedDefault, err := envBool("TLS_SELF_SIGNED")
	if err != nil {
		return err
	}
	fileMode, err := parseMode("FILE_MODE", "0640")
	if err != nil {
		return err
	}
	dirMode, err := parseMode("DIR_MODE", "0750")
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("filedeck", flag.ContinueOnError)
	root := flags.String("root", env("ROOT", ""), "directory of the own space \"files\" (FILEDECK_ROOT)")
	spacesDir := flags.String("spaces-dir", env("SPACES_DIR", ""), "each directory inside becomes a space named after it, e.g. mounts at /spaces/<name> (FILEDECK_SPACES_DIR)")
	space := flags.String("space", ownSpace, "space used by list/read/put")
	state := flags.String("state", env("STATE", ""), "existing private directory, mode 0700, outside every space (FILEDECK_STATE)")
	listen := flags.String("listen", env("LISTEN", "127.0.0.1:8080"), "listen address (FILEDECK_LISTEN)")
	origin := flags.String("origin", env("ORIGIN", ""), "exact public origin as typed in the browser, e.g. https://files.example.org (FILEDECK_ORIGIN)")
	insecure := flags.Bool("insecure-local", insecureDefault, "allow HTTP only on loopback for development (FILEDECK_INSECURE_LOCAL)")
	cert := flags.String("tls-cert", env("TLS_CERT", ""), "TLS certificate file (FILEDECK_TLS_CERT)")
	key := flags.String("tls-key", env("TLS_KEY", ""), "TLS private key file (FILEDECK_TLS_KEY)")
	selfSigned := flags.Bool("tls-self-signed", selfSignedDefault, "create and reuse a self-signed certificate in state (FILEDECK_TLS_SELF_SIGNED)")
	proxy := flags.String("proxy-cidr", env("PROXY_CIDR", ""), "trusted reverse-proxy peer CIDR; forwarded identity headers are ignored (FILEDECK_PROXY_CIDR)")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: filedeck [flags] list [PATH] | read PATH | put SOURCE TARGET | bootstrap USER | reset-password USER | reset-2fa USER | serve | healthcheck | selftest DIR\nbootstrap and reset-password read the password from stdin; reset-2fa turns off two-factor authentication for a user who lost their device. Place flags before the command. Every flag can also be set as FILEDECK_<NAME>;\nFILEDECK_FILE_MODE / FILEDECK_DIR_MODE (octal, default 0640 / 0750) set modes of created files and folders;\nFILEDECK_SECRET_KEY (or FILEDECK_SECRET_KEY_FILE) encrypts two-factor secrets: openssl rand -base64 32.")
		flags.PrintDefaults()
	}
	if err = flags.Parse(args); err != nil {
		return err
	}
	rest := flags.Args()
	if len(rest) == 1 && rest[0] == "healthcheck" {
		return healthcheck(*listen, *origin, *insecure || (*cert == "" && !*selfSigned))
	}
	if len(rest) == 2 && rest[0] == "selftest" {
		unix.Umask(0)
		return selftest(rest[1], storage.Modes{File: fileMode, Dir: dirMode}, os.Stdout)
	}
	spaces, err := discoverSpaces(*root, *spacesDir)
	if err != nil {
		return err
	}
	if len(spaces) == 0 || *state == "" || len(rest) == 0 {
		flags.Usage()
		return errors.New("state, at least one space (root or spaces-dir) and a command are required")
	}
	// Modes are applied exactly as configured.
	unix.Umask(0)
	// Create the private state and the own space on first start, owned by the
	// user the process actually runs as (e.g. FILEDECK_USER in Docker). Only
	// the final path component is created; existing entries are left as they
	// are and verified later (a symlink or wrong owner is rejected there).
	if err = ensureDir(*state, 0700); err != nil {
		return fmt.Errorf("state: %w", err)
	}
	if *root != "" {
		if err = ensureDir(*root, dirMode); err != nil {
			return fmt.Errorf("root: %w", err)
		}
	}
	if !((rest[0] == "list" && len(rest) <= 2) || (rest[0] == "read" && len(rest) == 2) || (rest[0] == "put" && len(rest) == 3) || ((rest[0] == "bootstrap" || rest[0] == "reset-password" || rest[0] == "reset-2fa") && len(rest) == 2) || (rest[0] == "serve" && len(rest) == 1)) {
		return errors.New("invalid command or arguments")
	}
	limits := core.DefaultLimits()
	svc, err := core.Open(*state, spaces, limits, storage.Modes{File: fileMode, Dir: dirMode})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, svc.Close()) }()
	if rest[0] == "bootstrap" || rest[0] == "reset-password" || rest[0] == "reset-2fa" {
		return accountCommand(*state, rest)
	}
	if rest[0] == "serve" {
		secret, e := secretKey()
		if e != nil {
			return e
		}
		return serve(svc, *state, limits, serverOptions{listen: *listen, origin: *origin, cert: *cert, key: *key, proxy: *proxy, insecure: *insecure, selfSigned: *selfSigned, secretKey: secret})
	}
	// The CLI runs with the operator's OS privileges. This is not authentication
	// for remote users; network adapters must supply their own trusted subject.
	const subject = "local-operator"
	if err = svc.SetPermissions(subject, map[string]core.Permission{core.AnySpace: core.AllPermissions}); err != nil {
		return err
	}
	ctx := context.Background()
	switch rest[0] {
	case "list":
		name := "."
		if len(rest) == 2 {
			name = rest[1]
		}
		entries, e := svc.List(ctx, subject, *space, name, 10000)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(entries)
	case "read":
		f, e := svc.Read(ctx, subject, *space, rest[1])
		if e != nil {
			return e
		}
		defer f.Close()
		_, e = io.Copy(os.Stdout, f)
		return e
	case "put":
		source, e := os.OpenFile(rest[1], os.O_RDONLY|unix.O_NONBLOCK, 0)
		if e != nil {
			return e
		}
		defer source.Close()
		stat, e := source.Stat()
		if e != nil {
			return e
		}
		if !stat.Mode().IsRegular() {
			return errors.New("source must be a regular file")
		}
		upload, e := svc.Begin(ctx, subject, *space, rest[2], stat.Size())
		if e != nil {
			return e
		}
		// Uploads survive Close for resume; a failed CLI put has no one to resume it.
		published := false
		defer func() {
			if !published {
				err = errors.Join(err, svc.Abort(subject, upload.ID))
			}
		}()
		for upload.Offset < upload.Size {
			previousOffset := upload.Offset
			upload, e = svc.Patch(ctx, subject, upload.ID, upload.Offset, io.LimitReader(source, min(limits.MaxChunkBytes, upload.Size-upload.Offset)))
			if e != nil {
				return e
			}
			if upload.Offset == previousOffset {
				return io.ErrUnexpectedEOF
			}
		}
		// Detect a source that grew; external in-place edits still cannot provide a
		// coherent snapshot, which this prototype deliberately does not promise.
		var extra [1]byte
		n, e := source.Read(extra[:])
		if n != 0 || (e != nil && !errors.Is(e, io.EOF)) {
			return errors.New("source changed or could not be read")
		}
		published, e = svc.Commit(ctx, subject, upload.ID)
		if published && e != nil {
			return fmt.Errorf("file published but durability confirmation failed: %w", e)
		}
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"published": published, "path": rest[2], "bytes": upload.Size})
	}
	return nil
}
func main() {
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "filedeck:", err)
		os.Exit(1)
	}
}
