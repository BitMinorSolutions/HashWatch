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
	flag.Parse()
	m := sim.New()
	m.ReportLED = *led
	if _, err := m.Listen(*addr); err != nil {
		log.Fatal(err)
	}
	log.Printf("simulated Avalon Nano 3 on %s", *addr)
	select {}
}
