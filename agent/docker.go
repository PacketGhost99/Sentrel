package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	pb "sentrel/gen/sentrelv1"
)

// dockerClient talks to the Docker Engine API over its local socket.
type dockerClient struct {
	http *http.Client
	mu   sync.Mutex
	prev map[string]cpuSample
}

type cpuSample struct{ total, system uint64 }

func newDockerClient() *dockerClient {
	tr := dockerTransport()
	if tr == nil {
		return nil
	}
	return &dockerClient{http: &http.Client{Transport: tr, Timeout: 10 * time.Second}, prev: map[string]cpuSample{}}
}

func (d *dockerClient) get(ctx context.Context, path string, v any) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://docker"+path, nil)
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("docker API %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

type dockerListItem struct {
	ID      string   `json:"Id"`
	Names   []string `json:"Names"`
	Image   string   `json:"Image"`
	State   string   `json:"State"`
	Status  string   `json:"Status"`
	Created int64    `json:"Created"`
}

type dockerInspect struct {
	RestartCount int `json:"RestartCount"`
	State        struct {
		Health *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
}

type dockerStats struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs  int    `json:"online_cpus"`
	} `json:"cpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
}

func (d *dockerClient) containers(ctx context.Context) ([]*pb.Container, error) {
	var list []dockerListItem
	if err := d.get(ctx, "/containers/json?all=1", &list); err != nil {
		return nil, err
	}
	out := make([]*pb.Container, len(list))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, it := range list {
		name := it.ID[:12]
		if len(it.Names) > 0 {
			name = strings.TrimPrefix(it.Names[0], "/")
		}
		c := &pb.Container{Id: it.ID[:12], Name: name, Image: it.Image, State: it.State, Status: it.Status, Created: it.Created}
		out[i] = c
		wg.Add(1)
		go func(id string, c *pb.Container) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()
			var ins dockerInspect
			if d.get(cctx, "/containers/"+id+"/json", &ins) == nil {
				c.RestartCount = uint32(ins.RestartCount)
				if ins.State.Health != nil {
					c.Health = ins.State.Health.Status
				}
			}
			if c.State != "running" {
				return
			}
			var st dockerStats
			if d.get(cctx, "/containers/"+id+"/stats?stream=false&one-shot=true", &st) != nil {
				return
			}
			cur := cpuSample{st.CPUStats.CPUUsage.TotalUsage, st.CPUStats.SystemUsage}
			d.mu.Lock()
			prev, ok := d.prev[id]
			d.prev[id] = cur
			d.mu.Unlock()
			if ok && cur.system > prev.system && cur.total >= prev.total {
				cpus := st.CPUStats.OnlineCPUs
				if cpus == 0 {
					cpus = 1
				}
				c.CpuPercent = float64(cur.total-prev.total) / float64(cur.system-prev.system) * float64(cpus) * 100
			}
			used := st.MemoryStats.Usage
			if v, ok := st.MemoryStats.Stats["inactive_file"]; ok && v < used {
				used -= v
			} else if v, ok := st.MemoryStats.Stats["cache"]; ok && v < used {
				used -= v
			}
			c.MemoryUsage, c.MemoryLimit = used, st.MemoryStats.Limit
		}(it.ID, c)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
