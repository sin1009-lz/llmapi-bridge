package scanner

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

var probePaths = []string{"/v1/models", "/models", "/api/models"}

type DiscoveredServer struct {
	IP              string `json:"ip"`
	Port            int    `json:"port"`
	BaseURL         string `json:"base_url"`
	DisableV1Prefix bool   `json:"disable_v1_prefix"`
}

type ProviderChecker interface {
	ExistsByBaseURL(baseURL string) bool
}

type Scanner struct {
	client  *http.Client
	ports   []int
	checker ProviderChecker
}

func New(checker ProviderChecker) *Scanner {
	return &Scanner{
		client:  &http.Client{Timeout: 1500 * time.Millisecond},
		ports:   []int{8080, 8081, 8085},
		checker: checker,
	}
}

type subnetInfo struct {
	network   net.IP
	broadcast net.IP
}

func (s *Scanner) getSubnets() []subnetInfo {
	interfaces, err := net.Interfaces()
	if err != nil {
		slog.Error("failed to get network interfaces", "error", err)
		return nil
	}

	var subnets []subnetInfo
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil {
				continue
			}
			mask := net.IPv4Mask(255, 255, 255, 0)
			network := ip4.Mask(mask)
			broadcast := make(net.IP, 4)
			for i := range broadcast {
				broadcast[i] = network[i] | ^mask[i]
			}
			subnets = append(subnets, subnetInfo{
				network:   network,
				broadcast: broadcast,
			})
		}
	}
	return subnets
}

func (s *Scanner) Scan() []DiscoveredServer {
	subnets := s.getSubnets()
	if len(subnets) == 0 {
		slog.Warn("no subnets found to scan")
		return nil
	}

	var allFound []DiscoveredServer
	foundMu := sync.Mutex{}
	seen := make(map[string]bool)
	sema := make(chan struct{}, 50)
	var wg sync.WaitGroup

	for _, subnet := range subnets {
		startIP := subnet.network.To4()
		broadcastIP := subnet.broadcast.To4()
		if startIP == nil || broadcastIP == nil {
			continue
		}

		startInt := uint32(startIP[0])<<24 | uint32(startIP[1])<<16 | uint32(startIP[2])<<8 | uint32(startIP[3])
		endInt := uint32(broadcastIP[0])<<24 | uint32(broadcastIP[1])<<16 | uint32(broadcastIP[2])<<8 | uint32(broadcastIP[3])

		for ipInt := startInt + 1; ipInt < endInt; ipInt++ {
			ip := net.IPv4(byte(ipInt>>24), byte(ipInt>>16), byte(ipInt>>8), byte(ipInt))
			for _, port := range s.ports {
				wg.Add(1)
				sema <- struct{}{}
				go func(ipStr string, port int) {
					defer wg.Done()
					defer func() { <-sema }()
					if server := s.checkServer(ipStr, port); server != nil {
						foundMu.Lock()
						if !seen[server.BaseURL] {
							seen[server.BaseURL] = true
							allFound = append(allFound, *server)
							slog.Info("discovered llama.cpp server", "ip", ipStr, "port", port)
						}
						foundMu.Unlock()
					}
				}(ip.String(), port)
			}
		}
	}
	wg.Wait()
	return allFound
}

func probePathToPrefix(path string) string {
	switch path {
	case "/v1/models":
		return ""
	case "/models":
		return ""
	case "/api/models":
		return "/api"
	default:
		return ""
	}
}

func (s *Scanner) checkServer(ip string, port int) *DiscoveredServer {
	baseURL := fmt.Sprintf("http://%s:%d", ip, port)

	if s.checker != nil && s.checker.ExistsByBaseURL(baseURL) {
		return nil
	}

	type probeResult struct {
		server    *DiscoveredServer
		probePath string
	}
	ch := make(chan *probeResult, len(probePaths))

	for _, path := range probePaths {
		go func(probePath string) {
			req, err := http.NewRequest("GET", baseURL+probePath, nil)
			if err != nil {
				ch <- nil
				return
			}
			req.Header.Set("Accept", "application/json")

			resp, err := s.client.Do(req)
			if err != nil {
				ch <- nil
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				ch <- nil
				return
			}

			var result struct {
				Data   []interface{} `json:"data"`
				Object string        `json:"object"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				ch <- nil
				return
			}

			if len(result.Data) == 0 {
				ch <- nil
				return
			}

			if s.checker != nil && s.checker.ExistsByBaseURL(baseURL) {
				ch <- nil
				return
			}

			pathPrefix := probePathToPrefix(probePath)
			finalBaseURL := baseURL + pathPrefix
			disableV1 := probePath != "/v1/models"

			ch <- &probeResult{
				probePath: probePath,
				server: &DiscoveredServer{
					IP:              ip,
					Port:            port,
					BaseURL:         finalBaseURL,
					DisableV1Prefix: disableV1,
				},
			}
		}(path)
	}

	for range probePaths {
		if r := <-ch; r != nil && r.server != nil {
			return r.server
		}
	}
	return nil
}
