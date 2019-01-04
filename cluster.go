package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type node struct {
	host string
	err  chan error
}

func prepCluster(config Config, done chan struct{}) ([]*node, error) {
	var nodes []*node

	hosts := strings.Split(config.Host, ",")

	for ni := 0; ni < config.Nodes; ni++ {
		join := hosts[0]
		if ni == 0 {
			join = ""
		}
		node, err := prepNode(hosts[ni], join, done)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
		time.Sleep(time.Second * 1)
	}
	return nodes, nil
}

func prepNode(host, join string, done chan struct{}) (*node, error) {
	n := node{
		host: host,
		err:  make(chan error),
	}

	workdir := fmt.Sprintf("cockroach-work-%d", rand.Int())
	pidfile := workdir + "/cockroach.pid"

	hostport := strings.Split(host, ":")
	hostname := hostport[0]
	portstr := "26257"
	if len(hostport) > 1 {
		portstr = hostport[1]
	}
	port, err := strconv.Atoi(portstr)
	if err != nil {
		return nil, err
	}
	httpport := port - 10000
	if port == 26257 {
		httpport = 8080
	}

	go func() {
		err := func() error {
			defer func() {
				log.Println(host, "cleaning up", workdir)

				args := []string{hostname, "cat " + pidfile + " | xargs kill -9"}
				log.Println(host, debugcmd("ssh", args))
				cmd := exec.Command("ssh", args...)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					log.Println(host, err)
				}

				args = []string{hostname, "rm", "-rf", workdir}
				log.Println(host, debugcmd("ssh", args))
				cmd = exec.Command("ssh", args...)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					log.Println(host, err)
				}
			}()

			ctx, cancel := context.WithCancel(context.Background())

			args := []string{hostname, "cockroach", "start", "--insecure",
				"--store", workdir,
				"--listen-addr", host,
				"--http-addr", fmt.Sprintf("%s:%d", hostname, httpport),
				"--pid-file", pidfile,
			}
			if join != "" {
				args = append(args, "--join", join)
			}
			log.Println(host, debugcmd("ssh", args))
			cmd := exec.CommandContext(ctx, "ssh", args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				return err
			}

			go func() {
				_ = <-done
				cancel()
			}()

			if err := cmd.Wait(); err != nil && err.Error() != "signal: killed" {
				return err
			}

			return nil
		}()

		n.err <- err
	}()

	return &n, nil
}

func waitCluster(nodes []*node) error {
	var err error

	for _, n := range nodes {
		e := <-n.err
		if e != nil {
			err = fmt.Errorf("%s %v", n.host, e)
			log.Println(err)
		} else {
			log.Printf("%s success\n", n.host)
		}
	}

	return err
}
