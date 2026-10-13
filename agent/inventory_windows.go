//go:build windows

package agent

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows/registry"

	pb "sentrel/gen/sentrelv1"
)

func packageManager() string { return "windows" }

// CollectInventory lists installed applications (registry) and pending
// Windows Updates (COM IUpdateSearcher). The WU search can take minutes, so
// it runs with a long timeout and never blocks metric collection.
func CollectInventory(ctx context.Context, refresh bool, _ privRunner) *pb.Inventory {
	inv := &pb.Inventory{TsMs: time.Now().UnixMilli(), PackageManager: "windows", SecurityUpdatesKnown: true}
	inv.Installed = installedApps()
	ups, err := searchUpdates(ctx, 15*time.Minute)
	if err != nil {
		inv.Error = "Windows Update search: " + err.Error()
	}
	inv.Upgradable = ups
	return inv
}

func installedApps() []*pb.Package {
	seen := map[string]bool{}
	var out []*pb.Package
	const uninst = `Software\Microsoft\Windows\CurrentVersion\Uninstall`
	type root struct {
		k    registry.Key
		path string
		arch string
	}
	roots := []root{
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`, "x64"},
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`, "x86"},
		// The current user (a foreground run); under LocalSystem this is SYSTEM's hive.
		{registry.CURRENT_USER, uninst, "user"},
	}
	// Per-user installs of every loaded user profile (needed when running as a service).
	if users, err := registry.OpenKey(registry.USERS, "", registry.ENUMERATE_SUB_KEYS); err == nil {
		sids, _ := users.ReadSubKeyNames(-1)
		users.Close()
		for _, sid := range sids {
			if strings.HasPrefix(sid, "S-1-5-21-") && !strings.HasSuffix(sid, "_Classes") {
				roots = append(roots, root{registry.USERS, sid + `\` + uninst, "user"})
			}
		}
	}
	for _, r := range roots {
		k, err := registry.OpenKey(r.k, r.path, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		subs, _ := k.ReadSubKeyNames(-1)
		for _, s := range subs {
			sk, err := registry.OpenKey(k, s, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			name, _, _ := sk.GetStringValue("DisplayName")
			ver, _, _ := sk.GetStringValue("DisplayVersion")
			pub, _, _ := sk.GetStringValue("Publisher")
			sysComp, _, _ := sk.GetIntegerValue("SystemComponent")
			parent, _, _ := sk.GetStringValue("ParentKeyName")
			winInst, _, _ := sk.GetIntegerValue("WindowsInstaller")
			sk.Close()
			if name == "" || sysComp == 1 || parent != "" {
				continue
			}
			if ver == "" && pub == "Microsoft Corporation" {
				continue // unversioned Microsoft entries are system components
			}
			key := name + "|" + ver
			if seen[key] {
				continue
			}
			seen[key] = true
			typ := "exe"
			if winInst == 1 {
				typ = "msi"
			}
			out = append(out, &pb.Package{Name: name, Version: ver, Arch: r.arch, Publisher: pub, Type: typ})
		}
		k.Close()
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// comCall runs fn on a dedicated OS thread with COM initialised.
func comCall(ctx context.Context, timeout time.Duration, fn func() error) error {
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("COM panic: %v", r)
			}
		}()
		if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
			var oe *ole.OleError
			if !errors.As(err, &oe) || oe.Code() != 1 { // S_FALSE: already initialised
				done <- err
				return
			}
		}
		defer ole.CoUninitialize()
		done <- fn()
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(timeout):
		return fmt.Errorf("timed out after %s", timeout)
	}
}

func dispatch(v *ole.VARIANT, err error) (*ole.IDispatch, error) {
	if err != nil {
		return nil, err
	}
	d := v.ToIDispatch()
	if d == nil {
		return nil, errors.New("nil dispatch")
	}
	return d, nil
}

func prop(d *ole.IDispatch, name string, args ...interface{}) (*ole.VARIANT, error) {
	return oleutil.GetProperty(d, name, args...)
}

func newSession() (*ole.IDispatch, error) {
	unk, err := oleutil.CreateObject("Microsoft.Update.Session")
	if err != nil {
		return nil, err
	}
	defer unk.Release()
	return unk.QueryInterface(ole.IID_IDispatch)
}

// wuUpdate is a pending update with its COM handle (valid on its thread).
type wuUpdate struct {
	pkg *pb.Package
	d   *ole.IDispatch
}

func search(session *ole.IDispatch, criteria string) ([]wuUpdate, error) {
	searcher, err := dispatch(oleutil.CallMethod(session, "CreateUpdateSearcher"))
	if err != nil {
		return nil, err
	}
	res, err := dispatch(oleutil.CallMethod(searcher, "Search", criteria))
	if err != nil {
		return nil, err
	}
	coll, err := dispatch(prop(res, "Updates"))
	if err != nil {
		return nil, err
	}
	cv, err := prop(coll, "Count")
	if err != nil {
		return nil, err
	}
	n := int(cv.Val)
	var out []wuUpdate
	for i := 0; i < n; i++ {
		item, err := dispatch(prop(coll, "Item", i))
		if err != nil {
			continue
		}
		info := wuInfo{}
		if v, err := prop(item, "Title"); err == nil {
			info.Title = v.ToString()
		}
		info.KBs = strCollection(item, "KBArticleIDs")
		info.CVEs = strCollection(item, "CveIDs")
		if v, err := prop(item, "MsrcSeverity"); err == nil && v.Value() != nil {
			info.MsrcSev = v.ToString()
		}
		if cats, err := dispatch(prop(item, "Categories")); err == nil {
			if c, err := prop(cats, "Count"); err == nil {
				for j := 0; j < int(c.Val); j++ {
					if cat, err := dispatch(prop(cats, "Item", j)); err == nil {
						if nm, err := prop(cat, "Name"); err == nil {
							info.Categories = append(info.Categories, nm.ToString())
						}
					}
				}
			}
		}
		p := packageFromUpdate(info)
		out = append(out, wuUpdate{pkg: p, d: item})
	}
	return out, nil
}

// strCollection reads an IStringCollection property (KBArticleIDs, CveIDs).
func strCollection(d *ole.IDispatch, name string) []string {
	coll, err := dispatch(prop(d, name))
	if err != nil {
		return nil
	}
	c, err := prop(coll, "Count")
	if err != nil {
		return nil
	}
	var out []string
	for i := 0; i < int(c.Val); i++ {
		if v, err := prop(coll, "Item", i); err == nil {
			out = append(out, v.ToString())
		}
	}
	return out
}

func searchUpdates(ctx context.Context, timeout time.Duration) ([]*pb.Package, error) {
	var out []*pb.Package
	err := comCall(ctx, timeout, func() error {
		s, err := newSession()
		if err != nil {
			return err
		}
		defer s.Release()
		ups, err := search(s, "IsInstalled=0 and IsHidden=0 and Type='Software'")
		if err != nil {
			return err
		}
		for _, u := range ups {
			out = append(out, u.pkg)
		}
		return nil
	})
	return out, err
}

// installUpdates downloads and installs pending updates. Filters: security
// only and/or specific KBs. dryRun lists what would be installed.
func installUpdates(ctx context.Context, timeout time.Duration, o UpgradeOptions, log func(string)) (rebootNeeded bool, err error) {
	err = comCall(ctx, timeout, func() error {
		s, err := newSession()
		if err != nil {
			return err
		}
		defer s.Release()
		log("searching for updates (this can take several minutes)")
		ups, err := search(s, "IsInstalled=0 and IsHidden=0 and Type='Software'")
		if err != nil {
			return err
		}
		wantKB := map[string]bool{}
		for _, p := range o.Packages {
			wantKB[strings.ToUpper(strings.TrimPrefix(strings.ToUpper(p), "KB"))] = true
		}
		collUnk, err := oleutil.CreateObject("Microsoft.Update.UpdateColl")
		if err != nil {
			return err
		}
		coll, err := collUnk.QueryInterface(ole.IID_IDispatch)
		collUnk.Release()
		if err != nil {
			return err
		}
		defer coll.Release()
		n := 0
		for _, u := range ups {
			if o.SecurityOnly && !u.pkg.Security {
				continue
			}
			if len(wantKB) > 0 && !wantKB[strings.TrimPrefix(u.pkg.Kb, "KB")] {
				continue
			}
			log(fmt.Sprintf("selected: %s %s", u.pkg.Kb, u.pkg.Name))
			if o.DryRun {
				n++
				continue
			}
			if v, err := prop(u.d, "EulaAccepted"); err == nil && !v.Value().(bool) {
				_, _ = oleutil.CallMethod(u.d, "AcceptEula")
			}
			if _, err := oleutil.CallMethod(coll, "Add", u.d); err == nil {
				n++
			}
		}
		if n == 0 {
			log("no matching updates")
			return nil
		}
		if o.DryRun {
			log(fmt.Sprintf("dry run: %d update(s) would be installed", n))
			return nil
		}
		log(fmt.Sprintf("downloading %d update(s)", n))
		dl, err := dispatch(oleutil.CallMethod(s, "CreateUpdateDownloader"))
		if err != nil {
			return err
		}
		if _, err := oleutil.PutProperty(dl, "Updates", coll); err != nil {
			return err
		}
		if _, err := oleutil.CallMethod(dl, "Download"); err != nil {
			return fmt.Errorf("download: %w", err)
		}
		log("installing")
		inst, err := dispatch(oleutil.CallMethod(s, "CreateUpdateInstaller"))
		if err != nil {
			return err
		}
		if _, err := oleutil.PutProperty(inst, "Updates", coll); err != nil {
			return err
		}
		res, err := dispatch(oleutil.CallMethod(inst, "Install"))
		if err != nil {
			return fmt.Errorf("install: %w", err)
		}
		code, _ := prop(res, "ResultCode")
		if rb, err := prop(res, "RebootRequired"); err == nil {
			rebootNeeded, _ = rb.Value().(bool)
		}
		// 2 = succeeded, 3 = succeeded with errors, 4 = failed, 5 = aborted
		switch code.Val {
		case 2:
			log("all updates installed")
		case 3:
			log("installed with errors")
		default:
			return fmt.Errorf("Windows Update result code %d", code.Val)
		}
		if rebootNeeded {
			log("a reboot is required to finish installing updates")
		}
		return nil
	})
	return rebootNeeded, err
}
