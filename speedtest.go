// Copyright 2016 Matt Martz <matt@sivel.net>
// All Rights Reserved.
//
//    Licensed under the Apache License, Version 2.0 (the "License"); you may
//    not use this file except in compliance with the License. You may obtain
//    a copy of the License at
//
//         http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
//    WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
//    License for the specific language governing permissions and limitations
//    under the License.

package main

import (
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kellydunn/golang-geo"
)

func errorf(text string, a ...interface{}) {
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	fmt.Printf(text, a...)
	os.Exit(1)
}

type CliFlags struct {
	List   bool
	Server int
}

type Speedtest struct {
	Configuration Configuration
	Servers       Servers
}

func NewSpeedtest() Speedtest {
	return Speedtest{}
}

// Fetch Speedtest.net Configuration
func (s *Speedtest) GetConfiguration() (Configuration, error) {
	res, err := http.Get("https://www.speedtest.net/speedtest-config.php")
	if err != nil {
		return s.Configuration, errors.New("Error retrieving Speedtest.net configuration")
	}
	settingsBody, _ := ioutil.ReadAll(res.Body)
	res.Body.Close()
	xml.Unmarshal(settingsBody, &s.Configuration)
	return s.Configuration, nil
}

// Fetch Speedtest.net Servers
func (s *Speedtest) GetServers(serverId int) (Servers, error) {
	res, err := http.Get("https://www.speedtest.net/speedtest-servers.php")
	if err != nil {
		return s.Servers, errors.New("Error retrieving Speedtest.net servers")
	}
	serversBody, _ := ioutil.ReadAll(res.Body)
	res.Body.Close()
	var allServers Servers
	xml.Unmarshal(serversBody, &allServers)
	if serverId != 0 {
		for _, server := range allServers.Servers {
			if server.ID == serverId {
				s.Servers.Servers = append(s.Servers.Servers, server)
			}
		}
	} else {
		s.Servers = allServers
	}

	return s.Servers, nil
}

type Client struct {
	IP        string  `xml:"ip,attr"`
	ISP       string  `xml:"isp,attr"`
	Latitude  float64 `xml:"lat,attr"`
	Longitude float64 `xml:"lon,attr"`
}

type ServerConfig struct {
	IgnoreIDs   string `xml:"ignoreids,attr"`
	ThreadCount string `xml:"threadcount,attr"`
}

type Times struct {
	DownloadOne   int `xml:"dl1,attr"`
	DownloadTwo   int `xml:"dl2,attr"`
	DownloadThree int `xml:"dl3,attr"`
	UploadOne     int `xml:"ul1,attr"`
	UploadTwo     int `xml:"ul2,attr"`
	UploadThree   int `xml:"ul3,attr"`
}

type Download struct {
	Length       int `xml:"testlength,attr"`
	PacketLength int `xml:"packetlength,attr"`
}

type Upload struct {
	Length       int `xml:"testlength,attr"`
	PacketLength int `xml:"packetlength,attr"`
}

type Latency struct {
	Length int `xml:"testlength,attr"`
}

type Configuration struct {
	Client       Client       `xml:"client"`
	ServerConfig ServerConfig `xml:"server-config"`
	Times        Times        `xml:"times"`
	Download     Download     `xml:"socket-download"`
	Upload       Upload       `xml:"socket-upload"`
	Latency      Latency      `xml:"socket-latency"`
}

type Server struct {
	CC        string  `xml:"cc,attr"`
	Country   string  `xml:"country,attr"`
	ID        int     `xml:"id,attr"`
	Latitude  float64 `xml:"lat,attr"`
	Longitude float64 `xml:"lon,attr"`
	Name      string  `xml:"name,attr"`
	Sponsor   string  `xml:"sponsor,attr"`
	URL       string  `xml:"url,attr"`
	URL2      string  `xml:"url2,attr"`
	Host      string  `xml:"host,attr"`
	Distance  float64
	Latency   time.Duration
}

type Servers struct {
	Servers []Server `xml:"servers>server"`
}

// Sort is a method on the function type, By, that sorts the argument slice according to the function.
func (s *Servers) SortServersByDistance() {
	ps := &serverSorter{
		servers: s.Servers,
		by: func(s1, s2 *Server) bool {
			return s1.Distance < s2.Distance
		},
	}
	sort.Sort(ps)
}

// Sort is a method on the function type, By, that sorts the argument slice according to the function.
func (s *Servers) SortServersByLatency() {
	ps := &serverSorter{
		servers: s.Servers,
		by: func(s1, s2 *Server) bool {
			// Latency should never be 0 unless we didn't test latency for that server
			if s1.Latency == 0 {
				return false
			}
			return s1.Latency < s2.Latency
		},
	}
	sort.Sort(ps)
}

// serverSorter joins a By function and a slice of Servers to be sorted.
type serverSorter struct {
	servers []Server
	by      func(s1, s2 *Server) bool // Closure used in the Less method.
}

// Len is part of sort.Interface.
func (s *serverSorter) Len() int {
	return len(s.servers)
}

// Swap is part of sort.Interface.
func (s *serverSorter) Swap(i, j int) {
	s.servers[i], s.servers[j] = s.servers[j], s.servers[i]
}

// Less is part of sort.Interface. It is implemented by calling the "by" closure in the sorter.
func (s *serverSorter) Less(i, j int) bool {
	return s.by(&s.servers[i], &s.servers[j])
}

// Tests the 5 closest servers latency, and returns the server with lowest latency
func (s *Servers) TestLatency() Server {
	var servers []Server
	s.SortServersByDistance()

	if len(s.Servers) >= 5 {
		servers = s.Servers[:5]
	} else {
		servers = s.Servers[:len(s.Servers)]
	}

	for i, server := range servers {
		conn, err := net.Dial("tcp", server.Host)
		if err != nil {
			continue
		}
		conn.Write([]byte("HI\n"))
		hello := make([]byte, 1024)
		conn.Read(hello)

		sum := time.Duration(0)
		for j := 0; j < 3; j++ {
			resp := make([]byte, 1024)
			start := time.Now()
			conn.Write([]byte(fmt.Sprintf("PING %d\n", start.UnixNano()/1000000)))
			conn.Read(resp)
			total := time.Since(start)
			sum += total
		}
		s.Servers[i].Latency = sum / 3
	}
	s.SortServersByLatency()
	return s.Servers[0]
}

func (s *Server) Downloader(ci chan int, co chan []int, wg *sync.WaitGroup) {
	defer wg.Done()

	conn, err := net.Dial("tcp", s.Host)
	if err != nil {
		errorf("\nCannot connect to %s\n", s.Host)
	}

	defer conn.Close()

	conn.Write([]byte("HI\n"))
	hello := make([]byte, 1024)
	conn.Read(hello)
	var ask int
	tmp := make([]byte, 1024)

	var out []int

	for size := range ci {
		fmt.Printf(".")
		remaining := size

		for remaining > 0 {

			if remaining > 1000000 {
				ask = 1000000
			} else {
				ask = remaining
			}
			down := 0

			conn.Write([]byte(fmt.Sprintf("DOWNLOAD %d\n", ask)))

			for down < ask {
				n, err := conn.Read(tmp)
				if err != nil {
					if err != io.EOF {
						fmt.Printf("ERR: %v\n", err)
					}
					break
				}
				down += n
			}
			out = append(out, down)
			remaining -= down

		}
		fmt.Printf(".")
	}

	go func(co chan []int, out []int) {
		co <- out
	}(co, out)

}

func (s *Server) TestDownload() (float64, time.Duration) {
	ci := make(chan int)
	co := make(chan []int)
	wg := new(sync.WaitGroup)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go s.Downloader(ci, co, wg)
	}

	sizes := []int{245388, 505544, 1118012, 1986284, 4468241, 7907740, 12407926, 17816816, 24262167, 31625365}
	start := time.Now()
	for _, size := range sizes {
		for i := 0; i < 4; i++ {
			ci <- size
		}
	}

	close(ci)
	wg.Wait()

	total := time.Since(start)
	fmt.Println()

	var totalSize int
	for i := 0; i < 8; i++ {
		chunks := <-co
		for _, chunk := range chunks {
			totalSize += chunk
		}
	}

	return float64(totalSize) * 8, total
}

func (s *Server) Uploader(ci chan int, co chan []int, wg *sync.WaitGroup) {
	defer wg.Done()

	conn, err := net.Dial("tcp", s.Host)
	if err != nil {
		errorf("\nCannot connect to %s\n", s.Host)
	}
	conn.Write([]byte("HI\n"))
	hello := make([]byte, 1024)
	conn.Read(hello)

	var give int
	var out []int
	for size := range ci {
		fmt.Printf(".")
		remaining := size

		for remaining > 0 {
			if remaining > 100000 {
				give = 100000
			} else {
				give = remaining
			}
			header := []byte(fmt.Sprintf("UPLOAD %d 0\n", give))
			data := make([]byte, give-len(header))

			conn.Write(header)
			conn.Write(data)
			up := make([]byte, 24)
			conn.Read(up)

			out = append(out, give)
			remaining -= give
		}
		fmt.Printf(".")
	}

	go func(co chan []int, out []int) {
		co <- out
	}(co, out)

}

func (s *Server) TestUpload() (float64, time.Duration) {
	ci := make(chan int)
	co := make(chan []int)
	wg := new(sync.WaitGroup)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go s.Uploader(ci, co, wg)
	}

	sizes := []int{32768, 65536, 131072, 262144, 524288, 1048576, 7340032}
	start := time.Now()
	var tmp int
	for _, size := range sizes {
		for i := 0; i < 4; i++ {
			tmp += size
			ci <- size
		}
	}
	close(ci)
	wg.Wait()

	total := time.Since(start)
	fmt.Println()

	var totalSize int
	for i := 0; i < 8; i++ {
		chunks := <-co
		for _, chunk := range chunks {
			totalSize += chunk
		}
	}

	return float64(totalSize) * 8, total
}

func main() {
	cliFlags := &CliFlags{}

	flag.BoolVar(&cliFlags.List, "list", false, "Display a list of speedtest.net servers sorted by distance")
	flag.IntVar(&cliFlags.Server, "server", 0, "Specify a server ID to test against")
	flag.Parse()

	speedtest := NewSpeedtest()

	// ALL THE CPUS!
	runtime.GOMAXPROCS(runtime.NumCPU())

	fmt.Println("Retrieving speedtest.net configuration...")
	config, err := speedtest.GetConfiguration()
	if err != nil {
		errorf(err.Error())
	}

	fmt.Printf("Testing from %s (%s)...\n", config.Client.ISP, config.Client.IP)

	fmt.Println("Retrieving speedtest.net server list...")
	servers, err := speedtest.GetServers(cliFlags.Server)
	if err != nil {
		errorf(err.Error())
	} else if len(servers.Servers) == 0 {
		errorf("Failed to retrieve servers or invalid server ID specified")
	}

	me := geo.NewPoint(config.Client.Latitude, config.Client.Longitude)

	for i, server := range servers.Servers {
		serverPoint := geo.NewPoint(server.Latitude, server.Longitude)
		distance := me.GreatCircleDistance(serverPoint)
		servers.Servers[i].Distance = distance
	}

	if cliFlags.List {
		servers.SortServersByDistance()
		for _, server := range servers.Servers {
			fmt.Printf("%5d) %s (%s, %s) [%0.2f km]\n", server.ID, server.Sponsor, server.Name, server.Country, server.Distance)
		}
		os.Exit(0)
	}

	fmt.Println("Selecting best server based on ping...")
	bestServer := servers.TestLatency()
	if bestServer.Latency == 0 {
		errorf("Unable to test server latency, this may be caused by a connection failure to %s\n", bestServer.Host)
	}

	fmt.Printf("Hosted by %s (%s) [%0.2f km]: %0.2f ms\n", bestServer.Sponsor, bestServer.Name, bestServer.Distance, float64(bestServer.Latency.Nanoseconds())/1000000.0)
	fmt.Printf("Testing Download Speed")
	downBits, downDuration := bestServer.TestDownload()
	fmt.Printf("Download: %0.2f Mbit/s\n", downBits/1000/1000/downDuration.Seconds())
	fmt.Printf("Testing Upload Speed")
	upBits, upDuration := bestServer.TestUpload()
	fmt.Printf("Upload: %0.2f Mbit/s\n", upBits/1000/1000/upDuration.Seconds())
}
