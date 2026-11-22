// Command avalon-sim runs a simulated Avalon Nano 3 API for trying Hashwatch without hardware.
package main

import (
	"flag"
	"log"

	"github.com/hashwatch/hashwatch/internal/sim"
)

func main() {
	addr := flag.String("listen", "127.0.0.1:4028", "address to listen on")
	led := flag.Bool("report-led", false, "report LEDUser[] like the Nano 3S")
	nano3s := flag.Bool("nano3s", false, "identify as an Avalon Nano 3S (Fan control available)")
	admin := flag.String("admin", "", "optional test-control address, e.g. 127.0.0.1:14029 (GET /block, /offline?on=1, /pool?down=1, /hash?f=0.5, /heat?c=6, /spike?c=9&s=300, /best?d=2.1e9, /state)")
	flag.Parse()
	m := sim.New()
	m.ReportLED = *led
	m.Nano3S = *nano3s
	if _, err := m.Listen(*addr); err != nil {
		log.Fatal(err)
	}
	if *admin != "" {
		if err := m.Admin(*admin); err != nil {
			log.Fatal(err)
		}
		log.Printf("test controls on http://%s", *admin)
	}
	log.Printf("simulated Avalon Nano 3 on %s", *addr)
	select {}
}
