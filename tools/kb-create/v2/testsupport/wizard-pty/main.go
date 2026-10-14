package main

import (
	"fmt"
	"os"

	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/wizard"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: wizard-pty <platform-root>")
		os.Exit(2)
	}
	source, err := catalog.Seal(catalog.Catalog{
		Channels: map[contracts.Channel]string{contracts.ChannelStable: "2.0.0"},
		Platforms: []catalog.PlatformBundle{{
			ID: "platform", Version: "2.0.0", Package: "@kb/platform", Tarball: "https://example.test/platform.tgz", SHA256: "platform",
			Host:     &contracts.HostSpec{Command: "kb-host", HealthURL: "http://127.0.0.1:4000/health"},
			Profiles: map[string]contracts.ServiceGraph{"default": {}},
		}},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err = wizard.RequestScenario(source, os.Args[1], "explore", "", wizard.IO{In: os.Stdin, Out: os.Stdout}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("WIZARD DONE")
}
