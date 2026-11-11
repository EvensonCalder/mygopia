package kopia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"mygopia/internal/config"
)

// ContentStats summarizes repository deduplicated content statistics from `kopia content stats --raw`.
type ContentStats struct {
	Count            int64 `json:"count"`
	TotalBytes       int64 `json:"totalBytes"`
	OriginalBytes    int64 `json:"originalBytes"`
	PackedBytes      int64 `json:"packedBytes"`
	UncompactedCount int64 `json:"uncompactedCount"`
	UncompactedBytes int64 `json:"uncompactedBytes"`
	DeletedCount     int64 `json:"deletedCount"`
	DeletedBytes     int64 `json:"deletedBytes"`
}

// CLIHelper runs safe, non-interactive `kopia` administrative commands against a connected config file.
// Passwords are already persisted by Kopia's keychain/password-persistence file during connect/create,
// so secrets are never passed on command-line arguments.
type CLIHelper struct {
	KopiaBin   string
	RcloneBin  string
	ConfigFile string
}

func (h CLIHelper) run(ctx context.Context, args ...string) ([]byte, error) {
	if h.KopiaBin == "" {
		return nil, fmt.Errorf("kopia binary not configured")
	}
	fullArgs := append([]string{"--config-file=" + h.ConfigFile, "--disable-file-logging"}, args...)
	cmd := exec.CommandContext(ctx, h.KopiaBin, fullArgs...)
	configureSubprocess(cmd)
	cmd.Env = config.SubprocessEnv(h.KopiaBin, h.RcloneBin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return stdout.Bytes(), nil
}

// GetMaintenanceInfo reads `kopia maintenance info --json`.
func (h CLIHelper) GetMaintenanceInfo(ctx context.Context) (*MaintenanceInfo, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	raw, err := h.run(cctx, "maintenance", "info", "--json")
	if err != nil {
		return nil, err
	}
	var out MaintenanceInfo
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetMaintenance updates repository maintenance owner and quick/full cycle settings.
func (h CLIHelper) SetMaintenance(ctx context.Context, owner string, enableQuick bool, quickInterval time.Duration, enableFull bool, fullInterval time.Duration) error {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := []string{"maintenance", "set"}
	if owner != "" {
		args = append(args, "--owner="+owner)
	}
	args = append(args, fmt.Sprintf("--enable-quick=%v", enableQuick))
	if quickInterval > 0 {
		args = append(args, "--quick-interval="+quickInterval.String())
	}
	args = append(args, fmt.Sprintf("--enable-full=%v", enableFull))
	if fullInterval > 0 {
		args = append(args, "--full-interval="+fullInterval.String())
	}
	_, err := h.run(cctx, args...)
	return err
}

// RunMaintenance executes quick or full repository maintenance (`kopia maintenance run [--full]`).
func (h CLIHelper) RunMaintenance(ctx context.Context, full bool) (string, error) {
	args := []string{"maintenance", "run"}
	if full {
		args = append(args, "--full")
	}
	raw, err := h.run(ctx, args...)
	return strings.TrimSpace(string(raw)), err
}

// GetContentStats reads deduplicated repository content statistics (`kopia content stats --raw`).
func (h CLIHelper) GetContentStats(ctx context.Context) (*ContentStats, error) {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	raw, err := h.run(cctx, "content", "stats", "--raw")
	if err != nil {
		return nil, err
	}
	return parseContentStatsRaw(string(raw)), nil
}

func parseContentStatsRaw(output string) *ContentStats {
	st := &ContentStats{}
	var hasPacked bool
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "Count:"):
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "Count:"))
			st.Count, _ = strconv.ParseInt(val, 10, 64)
		case strings.HasPrefix(trimmed, "Total Bytes:"):
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "Total Bytes:"))
			if fields := strings.Fields(val); len(fields) > 0 {
				st.OriginalBytes, _ = strconv.ParseInt(fields[0], 10, 64)
				st.TotalBytes = st.OriginalBytes
			}
		case strings.HasPrefix(trimmed, "Total Packed:"):
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "Total Packed:"))
			if fields := strings.Fields(val); len(fields) > 0 {
				st.PackedBytes, _ = strconv.ParseInt(fields[0], 10, 64)
				hasPacked = true
			}
		}
	}
	if !hasPacked {
		st.PackedBytes = st.OriginalBytes
	}
	return st
}
