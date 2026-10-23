package cli

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steipete/camsnap/internal/config"
	"github.com/steipete/camsnap/internal/discovery"
)

var discoverDevices = discovery.Discover

func newDiscoverCmd() *cobra.Command {
	var timeout time.Duration
	var includeInfo bool
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "Discover cameras on the local network via ONVIF WS-Discovery",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sty := newStyler(cmd.OutOrStdout())
			discoverCtx, discoverCancel := context.WithTimeout(cmd.Context(), timeout)
			devs, err := discoverDevices(discoverCtx, timeout)
			discoverCancel()
			if cmd.Context().Err() != nil {
				return cmd.Context().Err()
			}
			if err != nil {
				return err
			}
			if len(devs) == 0 {
				cmd.Println(sty.Warn("No devices found. Ensure cameras and this host are on the same LAN."))
				return nil
			}
			cfg, _, _ := loadConfigFromFlag(cmd)
			for _, d := range devs {
				infoStr := ""
				if includeInfo {
					infoCtx, infoCancel := context.WithTimeout(cmd.Context(), timeout)
					info, infoErr := fetchInfo(infoCtx, cfg, d)
					infoCancel()
					switch {
					case infoErr != nil:
						hint := "; check the ONVIF endpoint and saved camera credentials"
						if errors.Is(infoErr, context.DeadlineExceeded) {
							hint = "; retry with a longer --timeout"
						}
						infoStr = " [info failed: " + infoErr.Error() + hint + "]"
					case info != "":
						infoStr = " [" + info + "]"
					}
				}
				host := hostOnly(d.Host)
				cmd.Printf("%s\t(add: camsnap add --name cam-%s --host %s --user <user> --pass <pass>)%s\n",
					sty.OK(d.Host), safeName(host), host, infoStr)
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Second, "Timeout for discovery and each device-information request")
	cmd.Flags().BoolVar(&includeInfo, "info", false, "Attempt ONVIF GetDeviceInformation (may require credentials)")
	return cmd
}

// The discovery port serves ONVIF, not RTSP; add supplies its own RTSP default.
func hostOnly(hostPort string) string {
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		return h
	}
	if strings.HasPrefix(hostPort, "[") && strings.HasSuffix(hostPort, "]") {
		return hostPort[1 : len(hostPort)-1]
	}
	return hostPort
}

func safeName(host string) string {
	return strings.ReplaceAll(host, ":", "-")
}

func fetchInfo(ctx context.Context, cfg config.Config, d discovery.Device) (string, error) {
	// If we already have creds for this host, try them first.
	user, pass := findCreds(cfg, d.Host)
	info, err := discovery.FetchDeviceInfo(ctx, d.Address, user, pass)
	if err != nil {
		return "", err
	}
	parts := []string{}
	if info.Model != "" {
		parts = append(parts, info.Model)
	}
	if info.Firmware != "" {
		parts = append(parts, "fw "+info.Firmware)
	}
	if info.Manufacturer != "" {
		parts = append(parts, info.Manufacturer)
	}
	return strings.Join(parts, ", "), nil
}

func findCreds(cfg config.Config, host string) (string, string) {
	for _, cam := range cfg.Cameras {
		if cam.Host == host || strings.HasPrefix(host, cam.Host+":") {
			return cam.Username, cam.Password
		}
	}
	return "", ""
}
