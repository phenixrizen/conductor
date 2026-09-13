package databaseops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Manifest contains no database address or credentials. Copy it with the archive;
// its checksum detects accidental changes, not authenticity against an attacker.
type Manifest struct {
	Version    int       `json:"version"`
	SHA256     string    `json:"sha256"`
	Bytes      int64     `json:"bytes"`
	PostgreSQL string    `json:"postgresql"`
	CreatedAt  time.Time `json:"createdAt"`
}

func serviceFile(databaseURL string) (string, func(), error) {
	u, err := url.Parse(databaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.User == nil || u.Fragment != "" {
		return "", nil, errors.New("operator database connection requires a PostgreSQL URL")
	}
	password, _ := u.User.Password()
	fields := map[string]string{"host": u.Hostname(), "port": u.Port(), "dbname": strings.TrimPrefix(u.Path, "/"), "user": u.User.Username(), "password": password, "connect_timeout": "10"}
	if fields["port"] == "" {
		fields["port"] = "5432"
	}
	allowed := map[string]bool{"sslmode": true, "sslrootcert": true, "sslcert": true, "sslkey": true, "sslcrl": true, "channel_binding": true, "target_session_attrs": true, "connect_timeout": true}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", nil, errors.New("invalid database parameters")
	}
	for key, values := range query {
		if !allowed[key] || len(values) != 1 {
			return "", nil, errors.New("unsupported or duplicate database parameter")
		}
		fields[key] = values[0]
	}
	if fields["sslmode"] == "" {
		fields["sslmode"] = "verify-full"
	}
	ip := net.ParseIP(fields["host"])
	if fields["sslmode"] != "verify-full" && (ip == nil || !ip.IsLoopback()) {
		return "", nil, errors.New("remote database archive operations require sslmode=verify-full")
	}
	keys := make([]string, 0, len(fields))
	for key, value := range fields {
		if strings.ContainsAny(value, "\r\n\x00") || strings.TrimSpace(value) != value {
			return "", nil, errors.New("unsupported database connection value")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	f, err := os.CreateTemp("", "conductor-pg-service-*")
	if err != nil {
		return "", nil, errors.New("private database service file unavailable")
	}
	cleanup := func() { os.Remove(f.Name()) }
	body := "[conductor-operator]\n"
	for _, key := range keys {
		body += key + "=" + fields[key] + "\n"
	}
	if _, err = f.WriteString(body); err != nil {
		f.Close()
		cleanup()
		return "", nil, errors.New("private database service file write failed")
	}
	if err = f.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return f.Name(), cleanup, nil
}
func archiveCommand(ctx context.Context, tool, service string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, tool, args...)
	// A fixed service reference avoids credentials in argv. No caller PG settings,
	// provider credentials or repository-controlled environment are inherited.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C", "PGSERVICEFILE=" + service, "PGSERVICE=conductor-operator"}
	return cmd
}
func checkTool(ctx context.Context, tool string) (string, error) {
	cmd := exec.CommandContext(ctx, tool, "--version")
	b, err := cmd.Output()
	version := strings.TrimSpace(string(b))
	if err != nil || len(b) > 256 || !strings.HasPrefix(version, filepath.Base(tool)+" (PostgreSQL) 17.") {
		return "", errors.New("PostgreSQL 17 archive tools are required")
	}
	return version, nil
}
func Backup(ctx context.Context, databaseURL, output string) (Manifest, error) {
	var m Manifest
	if !filepath.IsAbs(output) {
		return m, errors.New("backup output must be an absolute new file")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		return m, errors.New("backup output already exists or is inaccessible")
	}
	if _, err := os.Lstat(output + ".json"); !os.IsNotExist(err) {
		return m, errors.New("backup manifest already exists or is inaccessible")
	}
	version, err := checkTool(ctx, "pg_dump")
	if err != nil {
		return m, err
	}
	service, cleanup, err := serviceFile(databaseURL)
	if err != nil {
		return m, err
	}
	defer cleanup()
	f, err := os.CreateTemp(filepath.Dir(output), ".conductor-backup-*")
	if err != nil {
		return m, errors.New("backup directory unavailable")
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	cmd := archiveCommand(ctx, "pg_dump", service, "--format=custom", "--no-owner", "--no-acl", "--no-password", "--lock-wait-timeout=10s")
	cmd.Stdout = io.MultiWriter(f, h)
	if err = cmd.Run(); err != nil {
		f.Close()
		return m, errors.New("database backup failed; no archive published")
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return m, err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return m, err
	}
	if err = f.Close(); err != nil {
		return m, err
	}
	m = Manifest{1, hex.EncodeToString(h.Sum(nil)), info.Size(), version, time.Now().UTC()}
	// Hard-link publication is exclusive: a racing backup cannot overwrite data.
	if err = os.Link(f.Name(), output); err != nil {
		return m, errors.New("backup output publication failed")
	}
	body, _ := json.MarshalIndent(m, "", "  ")
	mf, err := os.OpenFile(output+".json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return m, errors.New("archive saved but manifest publication failed")
	}
	_, writeErr := mf.Write(append(body, '\n'))
	syncErr := mf.Sync()
	closeErr := mf.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return m, errors.New("archive saved but manifest write failed")
	}
	return m, nil
}
func Restore(ctx context.Context, databaseURL, input string) error {
	if !filepath.IsAbs(input) {
		return errors.New("restore input must be an absolute archive path")
	}
	info, err := os.Lstat(input)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 5 {
		return errors.New("restore archive must be a nonempty regular file")
	}
	mf, err := os.OpenFile(input+".json", os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("restore manifest unavailable")
	}
	mi, err := mf.Stat()
	if err != nil || !mi.Mode().IsRegular() || mi.Size() > 4096 {
		mf.Close()
		return errors.New("restore manifest must be a bounded regular file")
	}
	body, err := io.ReadAll(io.LimitReader(mf, 4097))
	mf.Close()
	if err != nil || len(body) > 4096 {
		return errors.New("restore manifest unavailable")
	}
	var m Manifest
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&m) != nil || decoder.Decode(&struct{}{}) != io.EOF || m.Version != 1 || m.Bytes != info.Size() || len(m.SHA256) != 64 {
		return errors.New("invalid restore manifest")
	}
	source, err := os.OpenFile(input, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("restore archive unreadable")
	}
	defer source.Close()
	sourceInfo, err := source.Stat()
	if err != nil || !sourceInfo.Mode().IsRegular() {
		return errors.New("restore archive is not a regular file")
	}
	f, err := os.CreateTemp("", "conductor-restore-*")
	if err != nil {
		return errors.New("private restore snapshot unavailable")
	}
	defer os.Remove(f.Name())
	defer f.Close()
	h := sha256.New()
	copied, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(source, m.Bytes+1))
	if copyErr != nil || copied != m.Bytes || hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
		return errors.New("restore archive checksum mismatch")
	}
	if _, err = checkTool(ctx, "pg_restore"); err != nil {
		return err
	}
	service, cleanup, err := serviceFile(databaseURL)
	if err != nil {
		return err
	}
	defer cleanup()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return errors.New("restore target unavailable")
	}
	defer conn.Close(context.Background())
	// Hold an operator lock through pg_restore; this serializes these restore tools.
	// The target must also be quarantined from applications by the operator.
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock(616065732114114)`); err != nil {
		return errors.New("restore lock unavailable")
	}
	var empty bool
	err = conn.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%' AND c.relkind IN ('r','p','v','m','S','f')) AND NOT EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%')`).Scan(&empty)
	if err != nil || !empty {
		return errors.New("restore requires an empty, quarantined target database")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return errors.New("restore archive seek failed")
	}
	// Read the private verified snapshot, unaffected by changes to the original archive.
	cmd := archiveCommand(ctx, "pg_restore", service, "--dbname=service=conductor-operator", "--format=custom", "--single-transaction", "--exit-on-error", "--no-owner", "--no-acl", "--no-password")
	cmd.Stdin = f
	if err = cmd.Run(); err != nil {
		return errors.New("database restore failed; transaction rolled back")
	}
	return nil
}
