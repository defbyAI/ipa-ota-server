package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	log.SetFlags(0)
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		log.Fatal(err)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, "Usage: ipa-ota-server publish|serve [flags]")
		return nil
	}
	switch args[0] {
	case "publish":
		flags := flag.NewFlagSet("publish", flag.ContinueOnError)
		ipa := flags.String("ipa", "", "Signed IPA to publish")
		directory := flags.String("directory", "", "Private publication directory")
		smallIcon := flags.String("small-icon", "", "Optional small PNG")
		largeIcon := flags.String("large-icon", "", "Optional large PNG")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *ipa == "" || *directory == "" || flags.NArg() != 0 {
			return errors.New("usage: ipa-ota-server publish --ipa FILE --directory DIR")
		}
		release, err := publish(*ipa, *directory, *smallIcon, *largeIcon)
		if err != nil {
			return err
		}
		output := json.NewEncoder(os.Stdout)
		output.SetIndent("", "  ")
		return output.Encode(release)
	case "serve":
		flags := flag.NewFlagSet("serve", flag.ContinueOnError)
		directory := flags.String("directory", "", "Private publication directory")
		bind := flags.String("bind", "127.0.0.1", "HTTP address to listen on")
		port := flags.Int("port", 8787, "HTTP port")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *directory == "" || flags.NArg() != 0 {
			return errors.New("usage: ipa-ota-server serve --directory DIR [--bind IP] [--port PORT]")
		}
		if *port < 0 || *port > 65535 {
			return errors.New("port must be between 0 and 65535")
		}
		return serve(*directory, net.JoinHostPort(*bind, strconv.Itoa(*port)))
	default:
		return fmt.Errorf("unknown command %q; use publish or serve", args[0])
	}
}

func serve(directory, address string) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()

	index, err := root.Stat("index.html")
	if err != nil || !index.Mode().IsRegular() {
		return errors.New("publish an IPA to this directory before serving it")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()

	server := &http.Server{
		Handler:           otaHandler{root: root},
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	log.Printf("Serving IPA releases at http://%s/", listener.Addr())
	return server.Serve(listener)
}
