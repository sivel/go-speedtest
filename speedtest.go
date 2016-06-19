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
	"crypto/md5"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kellydunn/golang-geo"
)

const (
	version = "0.0.1"
)

// Helper function to make it easier for printing and exiting
func errorf(text string, a ...interface{}) {
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	fmt.Printf(text, a...)
	os.Exit(1)
}

// Established connection with local address and timeout support
func dialTimeout(network string, laddr *net.TCPAddr, raddr *net.TCPAddr, timeout time.Duration) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout:   timeout,
		LocalAddr: laddr,
	}

	conn, err := dialer.Dial(network, raddr.String())
	return conn, err
}

type CliFlags struct {
	List        bool
	Server      int
	Interactive bool // Not a direct flag, this is derived from whether a user has or has not selected a machine readable output
	Json        bool
	Xml         bool
	Csv         bool
	Simple      bool
	Source      string
	Timeout     int64
	Share       bool
	Version     bool
}

func NewCliFlags() *CliFlags {
	return &CliFlags{
		Interactive: true,
	}
}

type Results struct {
	XMLName   xml.Name  `json:"-" xml:"results"`
	Download  float64   `json:"download" xml:"download"`
	Upload    float64   `json:"upload" xml:"upload"`
	Latency   float64   `json:"latency" xml:"latency"`
	Server    *Server   `json:"server" xml:"server"`
	Timestamp time.Time `json:"timestamp" xml:"timestamp"`
	Share     string    `json:"share" xml:"share"`
}

func NewResults() *Results {
	return &Results{
		Timestamp: time.Now(),
	}
}

// Marshall results to JSON and print
func (r *Results) ToJson() {
	out, err := json.MarshalIndent(r, "", "    ")
	if err != nil {
		errorf(err.Error())
	}
	fmt.Println(string(out))
}

// Marshal results to XML and print
func (r *Results) ToXml() {
	out, err := xml.MarshalIndent(r, "", "    ")
	if err != nil {
		errorf(err.Error())
	}
	fmt.Printf("%s%s", xml.Header, string(out))
}

// Output results as CSV
// Format is:
//    ID,Sponsor,Name,Timestamp,Distance (km),Latency (ms),Download (bits/s),Upload (bits/s)
func (r *Results) ToCsv() {
	record := []string{
		strconv.Itoa(r.Server.ID),
		r.Server.Sponsor,
		r.Server.Name,
		r.Timestamp.Format(time.RFC3339),
		strconv.FormatFloat(r.Server.Distance, 'f', -1, 64),
		strconv.FormatFloat(r.Latency, 'f', -1, 64),
		strconv.FormatFloat(r.Download, 'f', -1, 64),
		strconv.FormatFloat(r.Upload, 'f', -1, 64),
	}
	w := csv.NewWriter(os.Stdout)
	w.Write(record)
	w.Flush()
}

// Output results in "simple" format
func (r *Results) ToSimple() {
	fmt.Printf("Latency: %.02f ms\n", r.Latency)
	fmt.Printf("Download: %.02f Mbit/s\n", r.Download/1000/1000)
	fmt.Printf("Upload: %.02f Mbit/s\n", r.Upload/1000/1000)
}

func (r *Results) ToPng() {
	kDownload := strconv.FormatFloat(r.Download/1000, 'f', 0, 64)
	kUpload := strconv.FormatFloat(r.Upload/1000, 'f', 0, 64)
	latency := strconv.FormatFloat(r.Latency, 'f', 0, 64)
	hashData := []byte(fmt.Sprintf("%s-%s-%s-297aae72", latency, kUpload, kDownload))
	hash := fmt.Sprintf("%x", md5.Sum(hashData))

	form := url.Values{}
	form.Add("download", kDownload)
	form.Add("ping", latency)
	form.Add("upload", kUpload)
	form.Add("promo", "")
	form.Add("startmode", "pingselect")
	form.Add("recommendedserverid", strconv.Itoa(r.Server.ID))
	form.Add("accuracy", "1")
	form.Add("serverid", strconv.Itoa(r.Server.ID))
	form.Add("hash", hash)

	req, _ := http.NewRequest("POST", "https://www.speedtest.net/api/api.php", strings.NewReader(form.Encode()))
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", "http://c.speedtest.net/flash/speedtest.swf")
	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		r.Share = "Could not submit results to: " + err.Error()
	}

	defer res.Body.Close()
	resBody, _ := ioutil.ReadAll(res.Body)
	qsValues, _ := url.ParseQuery(string(resBody))
	r.Share = fmt.Sprintf("http://www.speedtest.net/result/%s.png", qsValues.Get("resultid"))
	r.Server.speedtest.Printf("Share results: %s", r.Share)
}

type Speedtest struct {
	Configuration *Configuration
	Servers       *Servers
	CliFlags      *CliFlags
	Results       *Results
	Source        *net.TCPAddr
	Timeout       time.Duration
}

func NewSpeedtest() *Speedtest {
	return &Speedtest{
		Configuration: &Configuration{},
		Servers:       &Servers{},
		CliFlags:      NewCliFlags(),
		Results:       NewResults(),
	}
}

// Printf helper that only prints in "interactive" mode
func (s *Speedtest) Printf(text string, a ...interface{}) {
	if !s.CliFlags.Interactive {
		return
	}

	fmt.Printf(text, a...)
}

// Fetch Speedtest.net Configuration
func (s *Speedtest) GetConfiguration() (*Configuration, error) {
	res, err := http.Get("https://www.speedtest.net/speedtest-config.php")
	if err != nil {
		return s.Configuration, errors.New("Error retrieving Speedtest.net configuration")
	}
	defer res.Body.Close()
	settingsBody, _ := ioutil.ReadAll(res.Body)
	xml.Unmarshal(settingsBody, &s.Configuration)
	return s.Configuration, nil
}

// Fetch Speedtest.net Servers
func (s *Speedtest) GetServers(serverId int) (*Servers, error) {
	res, err := http.Get("https://www.speedtest.net/speedtest-servers.php")
	if err != nil {
		return s.Servers, errors.New("Error retrieving Speedtest.net servers")
	}
	defer res.Body.Close()
	serversBody, _ := ioutil.ReadAll(res.Body)
	var allServers Servers
	xml.Unmarshal(serversBody, &allServers)
	for _, server := range allServers.Servers {
		server.speedtest = s
		if serverId == 0 || server.ID == serverId {
			s.Servers.Servers = append(s.Servers.Servers, server)
		}
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
	Length       float64 `xml:"testlength,attr"`
	PacketLength int     `xml:"packetlength,attr"`
}

type Upload struct {
	Length       float64 `xml:"testlength,attr"`
	PacketLength int     `xml:"packetlength,attr"`
}

type Latency struct {
	Length float64 `xml:"testlength,attr"`
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
	CC        string        `xml:"cc,attr" json:"cc"`
	Country   string        `xml:"country,attr" json:"country"`
	ID        int           `xml:"id,attr" json:"id"`
	Latitude  float64       `xml:"lat,attr" json:"lat"`
	Longitude float64       `xml:"lon,attr" json:"lon"`
	Name      string        `xml:"name,attr" json:"name"`
	Sponsor   string        `xml:"sponsor,attr" json:"sponsor"`
	URL       string        `xml:"url,attr" json:"url"`
	URL2      string        `xml:"url2,attr" json:"url2"`
	Host      string        `xml:"host,attr" json:"host"`
	Distance  float64       `xml:"distance,attr" json:"distance"`
	Latency   time.Duration `xml:"latency,attr" json:"latency"`
	speedtest *Speedtest
	tcpAddr   *net.TCPAddr
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

// Calculates the distance to all servers
func (s *Servers) SetDistances(latitude, longitude float64) {
	me := geo.NewPoint(latitude, longitude)
	for i, server := range s.Servers {
		serverPoint := geo.NewPoint(server.Latitude, server.Longitude)
		distance := me.GreatCircleDistance(serverPoint)
		s.Servers[i].Distance = distance
	}
}

// Tests the 5 closest servers latency, and returns the server with lowest latency
func (s *Servers) TestLatency() *Server {
	var servers []Server
	s.SortServersByDistance()

	if len(s.Servers) >= 5 {
		servers = s.Servers[:5]
	} else {
		servers = s.Servers[:len(s.Servers)]
	}

	for i, server := range servers {
		addr, err := net.ResolveTCPAddr("tcp", server.Host)
		s.Servers[i].tcpAddr = addr
		if err != nil {
			server.speedtest.Printf("%s\n", err.Error())
			continue
		}

		conn, err := dialTimeout("tcp", server.speedtest.Source, addr, server.speedtest.Timeout)
		if err != nil {
			server.speedtest.Printf("%s\n", err.Error())
			continue
		}

		defer conn.Close()

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
	return &s.Servers[0]
}

// Goroutine for downloading data
func (s *Server) Downloader(ci chan int, co chan []int, wg *sync.WaitGroup, start time.Time, length float64) {
	defer wg.Done()

	conn, err := dialTimeout("tcp", s.speedtest.Source, s.tcpAddr, s.speedtest.Timeout)
	if err != nil {
		errorf("\nCannot connect to %s\n", s.tcpAddr.String())
	}

	defer conn.Close()

	conn.Write([]byte("HI\n"))
	hello := make([]byte, 1024)
	conn.Read(hello)
	var ask int
	tmp := make([]byte, 1024)

	var out []int

	for size := range ci {
		s.speedtest.Printf(".")
		remaining := size

		for remaining > 0 && time.Since(start).Seconds() < length {

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
		s.speedtest.Printf(".")
	}

	go func(co chan []int, out []int) {
		co <- out
	}(co, out)

}

// Function that controls Downloader goroutine
func (s *Server) TestDownload(length float64) (float64, time.Duration) {
	ci := make(chan int)
	co := make(chan []int)
	wg := new(sync.WaitGroup)
	sizes := []int{245388, 505544, 1118012, 1986284, 4468241, 7907740, 12407926, 17816816, 24262167, 31625365}
	start := time.Now()

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go s.Downloader(ci, co, wg, start, length)
	}

	for _, size := range sizes {
		for i := 0; i < 4; i++ {
			ci <- size
		}
	}

	close(ci)
	wg.Wait()

	total := time.Since(start)
	s.speedtest.Printf("\n")

	var totalSize int
	for i := 0; i < 8; i++ {
		chunks := <-co
		for _, chunk := range chunks {
			totalSize += chunk
		}
	}

	return float64(totalSize) * 8, total
}

// Goroutine for uploading data
func (s *Server) Uploader(ci chan int, co chan []int, wg *sync.WaitGroup, start time.Time, length float64) {
	defer wg.Done()

	conn, err := dialTimeout("tcp", s.speedtest.Source, s.tcpAddr, s.speedtest.Timeout)
	if err != nil {
		errorf("\nCannot connect to %s\n", s.tcpAddr.String())
	}

	defer conn.Close()

	conn.Write([]byte("HI\n"))
	hello := make([]byte, 1024)
	conn.Read(hello)

	var give int
	var out []int
	for size := range ci {
		s.speedtest.Printf(".")
		remaining := size

		for remaining > 0 && time.Since(start).Seconds() < length {
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
		s.speedtest.Printf(".")
	}

	go func(co chan []int, out []int) {
		co <- out
	}(co, out)

}

// Function that controls Uploader goroutine
func (s *Server) TestUpload(length float64) (float64, time.Duration) {
	ci := make(chan int)
	co := make(chan []int)
	wg := new(sync.WaitGroup)
	sizes := []int{32768, 65536, 131072, 262144, 524288, 1048576, 7340032}
	start := time.Now()

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go s.Uploader(ci, co, wg, start, length)
	}

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
	s.speedtest.Printf("\n")

	var totalSize int
	for i := 0; i < 8; i++ {
		chunks := <-co
		for _, chunk := range chunks {
			totalSize += chunk
		}
	}

	return float64(totalSize) * 8, total
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage: %s [options]

Command line interface for testing internet bandwidth using speedtest.net.
--------------------------------------------------------------------------
https://github.com/sivel/speedtest

options:
`, path.Base(os.Args[0]))
	flag.PrintDefaults()
	os.Exit(2)
}

func printVersion() {
	fmt.Println(version)
	os.Exit(0)
}

func main() {
	speedtest := NewSpeedtest()

	flag.Usage = usage
	flag.BoolVar(&speedtest.CliFlags.Json, "json", false, "Suppress verbose output, only show basic information in JSON format")
	flag.BoolVar(&speedtest.CliFlags.Xml, "xml", false, "Suppress verbose output, only show basic information in XML format")
	flag.BoolVar(&speedtest.CliFlags.Csv, "csv", false, "Suppress verbose output, only show basic information in CSV format")
	flag.BoolVar(&speedtest.CliFlags.Simple, "simple", false, "Suppress verbose output, only show basic information")
	flag.BoolVar(&speedtest.CliFlags.List, "list", false, "Display a list of speedtest.net servers sorted by distance")
	flag.BoolVar(&speedtest.CliFlags.Share, "share", false, "Generate and provide a URL to the speedtest.net share results image")
	flag.BoolVar(&speedtest.CliFlags.Version, "version", false, "Show the version number and exit")
	flag.IntVar(&speedtest.CliFlags.Server, "server", 0, "Specify a server ID to test against")
	flag.StringVar(&speedtest.CliFlags.Source, "source", "", "Source IP address to bind to")
	flag.Int64Var(&speedtest.CliFlags.Timeout, "timeout", 10, "Timeout in seconds")
	flag.Parse()

	if speedtest.CliFlags.Version {
		printVersion()
	}

	speedtest.Timeout = time.Duration(speedtest.CliFlags.Timeout) * time.Second

	if speedtest.CliFlags.Source != "" {
		source, err := net.ResolveTCPAddr("tcp", speedtest.CliFlags.Source+":0")
		if err != nil {
			errorf("Could not parse source IP address %s: %s", speedtest.CliFlags.Source, err.Error())
		} else {
			speedtest.Source = source
		}
	} else {
		speedtest.Source = nil
	}

	if speedtest.CliFlags.Json || speedtest.CliFlags.Xml || speedtest.CliFlags.Csv || speedtest.CliFlags.Simple {
		speedtest.CliFlags.Interactive = false
	}

	// ALL THE CPUS!
	runtime.GOMAXPROCS(runtime.NumCPU())

	speedtest.Printf("Retrieving speedtest.net configuration...\n")
	config, err := speedtest.GetConfiguration()
	if err != nil {
		errorf(err.Error())
	}

	speedtest.Printf("Testing from %s (%s)...\n", config.Client.ISP, config.Client.IP)

	speedtest.Printf("Retrieving speedtest.net server list...\n")
	servers, err := speedtest.GetServers(speedtest.CliFlags.Server)
	if err != nil {
		errorf(err.Error())
	} else if len(servers.Servers) == 0 {
		errorf("Failed to retrieve servers or invalid server ID specified")
	}

	servers.SetDistances(config.Client.Latitude, config.Client.Longitude)

	if speedtest.CliFlags.List {
		servers.SortServersByDistance()
		for _, server := range servers.Servers {
			speedtest.Printf("%5d) %s (%s, %s) [%0.2f km]\n", server.ID, server.Sponsor, server.Name, server.Country, server.Distance)
		}
		os.Exit(0)
	}

	speedtest.Printf("Selecting best server based on latency...\n")
	speedtest.Results.Server = servers.TestLatency()
	speedtest.Results.Latency = float64(speedtest.Results.Server.Latency.Nanoseconds()) / 1000000.0
	if speedtest.Results.Server.Latency == 0 {
		errorf("Unable to test server latency, this may be caused by a connection failure")
	}

	speedtest.Printf("Hosted by %s (%s) [%0.2f km]: %0.2f ms\n", speedtest.Results.Server.Sponsor, speedtest.Results.Server.Name, speedtest.Results.Server.Distance, float64(speedtest.Results.Server.Latency.Nanoseconds())/1000000.0)

	speedtest.Printf("Testing Download Speed")
	downBits, downDuration := speedtest.Results.Server.TestDownload(config.Download.Length)
	speedtest.Results.Download = downBits / downDuration.Seconds()
	speedtest.Printf("Download: %0.2f Mbit/s\n", speedtest.Results.Download/1000/1000)

	speedtest.Printf("Testing Upload Speed")
	upBits, upDuration := speedtest.Results.Server.TestUpload(config.Upload.Length)
	speedtest.Results.Upload = upBits / upDuration.Seconds()
	speedtest.Printf("Upload: %0.2f Mbit/s\n", speedtest.Results.Upload/1000/1000)

	if speedtest.CliFlags.Share {
		speedtest.Results.ToPng()
	}

	if speedtest.CliFlags.Json {
		speedtest.Results.ToJson()
	} else if speedtest.CliFlags.Xml {
		speedtest.Results.ToXml()
	} else if speedtest.CliFlags.Csv {
		speedtest.Results.ToCsv()
	} else if speedtest.CliFlags.Simple {
		speedtest.Results.ToSimple()
	}
}
