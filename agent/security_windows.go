//go:build windows

package agent

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func watchAuthLog(ctx context.Context, fc *failureCounter, log *slog.Logger) string { return "" }

type winEvents struct {
	Events []struct {
		RecordID int64 `xml:"System>EventRecordID"`
		Data     []struct {
			Name  string `xml:"Name,attr"`
			Value string `xml:",chardata"`
		} `xml:"EventData>Data"`
	} `xml:"Event"`
}

func queryFailedLogons(ctx context.Context, after int64, count int) (*winEvents, error) {
	q := fmt.Sprintf("*[System[(EventID=4625) and (EventRecordID > %d)]]", after)
	args := []string{"qe", "Security", "/q:" + q, "/f:xml", "/c:" + strconv.Itoa(count)}
	if after == 0 {
		args = append(args, "/rd:true") // newest first, to find the baseline
	}
	out, err := runOut(ctx, time.Minute, "wevtutil", args...)
	if err != nil {
		return nil, fmt.Errorf("wevtutil: %w", err)
	}
	var ev winEvents
	if err := xml.Unmarshal([]byte("<Events>"+string(out)+"</Events>"), &ev); err != nil {
		return nil, err
	}
	return &ev, nil
}

// watchWindowsSecurity polls the Security log for failed logons (4625).
func watchWindowsSecurity(ctx context.Context, fc *failureCounter, log *slog.Logger) string {
	// Probe synchronously so the caller never reports detection as active
	// when the Security log cannot be read.
	probe, err := queryFailedLogons(ctx, 0, 1)
	if err != nil {
		reason := err.Error()
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 5 {
			reason = "access denied (wevtutil exit status 5)"
		}
		log.Warn("brute-force detection unavailable: cannot read the Windows Security event log (run the agent as a service/LocalSystem or as Administrator)", "reason", reason)
		return ""
	}
	// Baseline: newest existing record, so history is not re-counted.
	var last int64
	if len(probe.Events) > 0 {
		last = probe.Events[0].RecordID
	}
	if last == 0 {
		last = 1
	}
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			ev, err := queryFailedLogons(ctx, last, 2000)
			if err != nil {
				continue
			}
			for _, e := range ev.Events {
				if e.RecordID > last {
					last = e.RecordID
				}
				var ip, user string
				for _, d := range e.Data {
					switch d.Name {
					case "IpAddress":
						ip = strings.TrimSpace(d.Value)
					case "TargetUserName":
						user = strings.TrimSpace(d.Value)
					}
				}
				if ip != "" && ip != "-" && ip != "::1" && ip != "127.0.0.1" {
					fc.Add(ip, user, 1)
				}
			}
		}
	}()
	return "windows-security-log"
}
