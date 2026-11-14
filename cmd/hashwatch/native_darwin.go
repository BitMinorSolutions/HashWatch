//go:build darwin

package main

// Native macOS shell without cgo: Cocoa + WebKit through the Objective-C runtime via purego.
// A window with a WKWebView on the local dashboard, a standard app menu, a menu bar item
// with live hashrate and a remote-access switch, and native notifications.

import (
	_ "embed"
	"errors"
	"fmt"
	"log"
	"runtime"
	"structs"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"github.com/hashwatch/hashwatch/internal/app"
	"github.com/hashwatch/hashwatch/internal/notify"
)

//go:embed assets/status@2x.png
var statusPNG []byte

func init() { runtime.LockOSThread() } // Cocoa must run on the main thread

func nativeSupported() bool { return inAppBundle() }

type nsPoint struct {
	_    structs.HostLayout
	X, Y float64
}
type nsSize struct {
	_    structs.HostLayout
	W, H float64
}
type nsRect struct {
	_      structs.HostLayout
	Origin nsPoint
	Size   nsSize
}

const (
	policyRegular   = 0
	policyAccessory = 1
	cmdKey          = 1 << 20
	optKey          = 1 << 19
)

var (
	selCache = map[string]objc.SEL{}
	selMu    sync.Mutex
)

func sel(n string) objc.SEL {
	selMu.Lock()
	defer selMu.Unlock()
	s, ok := selCache[n]
	if !ok {
		s = objc.RegisterName(n)
		selCache[n] = s
	}
	return s
}
func cls(n string) objc.ID { return objc.ID(objc.GetClass(n)) }
func nsstr(s string) objc.ID {
	return cls("NSString").Send(sel("stringWithUTF8String:"), s)
}
func gostr(id objc.ID) string {
	if id == 0 {
		return ""
	}
	p := objc.Send[*byte](id, sel("UTF8String"))
	if p == nil {
		return ""
	}
	var b []byte
	for q := unsafe.Pointer(p); *(*byte)(q) != 0; q = unsafe.Add(q, 1) {
		b = append(b, *(*byte)(q))
	}
	return string(b)
}

// callBlock invokes an Objective-C block that Cocoa handed us (e.g. a completion handler).
func callBlock(b objc.ID, args ...uintptr) {
	if b == 0 {
		return
	}
	p := *(*unsafe.Pointer)(unsafe.Pointer(&b)) // the block's address, as a pointer
	invoke := *(*uintptr)(unsafe.Add(p, 16))    // struct Block_layout { isa; int flags, reserved; invoke; ... }
	purego.SyscallN(invoke, append([]uintptr{uintptr(b)}, args...)...)
}

type macShell struct {
	o        shellOpts
	nsApp    objc.ID
	delegate objc.ID
	win      objc.ID
	web      objc.ID
	status   objc.ID
	itHash   objc.ID
	itDetail objc.ID
	itRemote objc.ID
}

var shell *macShell

func runNative(o shellOpts) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("native window unavailable (%v); using the browser", r)
			ok = false
		}
	}()
	for _, fw := range []string{"/System/Library/Frameworks/Cocoa.framework/Cocoa", "/System/Library/Frameworks/WebKit.framework/WebKit"} {
		if _, err := purego.Dlopen(fw, purego.RTLD_GLOBAL|purego.RTLD_LAZY); err != nil {
			log.Printf("native window unavailable (%v); using the browser", err)
			return false
		}
	}
	_, _ = purego.Dlopen("/System/Library/Frameworks/UserNotifications.framework/UserNotifications", purego.RTLD_GLOBAL|purego.RTLD_LAZY)
	if err := registerClasses(); err != nil {
		log.Printf("native window unavailable (%v); using the browser", err)
		return false
	}
	shell = &macShell{o: o}
	s := shell
	s.nsApp = cls("NSApplication").Send(sel("sharedApplication"))
	s.delegate = cls("HWAppDelegate").Send(sel("new"))
	s.nsApp.Send(sel("setDelegate:"), s.delegate)
	if o.Hidden {
		s.nsApp.Send(sel("setActivationPolicy:"), policyAccessory)
	} else {
		s.nsApp.Send(sel("setActivationPolicy:"), policyRegular)
	}
	s.buildMainMenu()
	s.buildStatusItem()
	s.buildWindow()
	s.setupNotifications()
	o.Srv.ShowWindow = func() { // from the HTTP server goroutine: hop to the main thread
		s.delegate.Send(sel("performSelectorOnMainThread:withObject:waitUntilDone:"), sel("showMain:"), objc.ID(0), false)
	}
	o.Srv.Shell = "macos-native"
	if !o.Hidden {
		s.show()
	}
	s.nsApp.Send(sel("run")) // never returns; Quit exits the process
	return true
}

func registerClasses() error {
	if objc.GetClass("HWAppDelegate") != 0 {
		return nil
	}
	m := func(name string, fn any) objc.MethodDef { return objc.MethodDef{Cmd: sel(name), Fn: fn} }
	_, err := objc.RegisterClass("HWAppDelegate", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
		m("applicationShouldHandleReopen:hasVisibleWindows:", func(self objc.ID, _ objc.SEL, _ objc.ID, _ bool) bool { shell.show(); return true }),
		m("applicationShouldTerminateAfterLastWindowClosed:", func(self objc.ID, _ objc.SEL, _ objc.ID) bool { return false }),
		m("applicationShouldTerminate:", func(self objc.ID, _ objc.SEL, _ objc.ID) uint { shell.o.Quit(); return 1 }),
		m("showMain:", func(self objc.ID, _ objc.SEL, _ objc.ID) { shell.show() }),
		m("openDashboard:", func(self objc.ID, _ objc.SEL, _ objc.ID) { shell.show() }),
		m("showSettings:", func(self objc.ID, _ objc.SEL, _ objc.ID) { shell.show(); shell.js(`location.hash = "#settings"`) }),
		m("reloadPage:", func(self objc.ID, _ objc.SEL, _ objc.ID) { shell.web.Send(sel("reload:"), objc.ID(0)) }),
		m("toggleRemote:", func(self objc.ID, _ objc.SEL, _ objc.ID) { shell.toggleRemote() }),
		m("showAbout:", func(self objc.ID, _ objc.SEL, _ objc.ID) { shell.about() }),
		m("menuWillOpen:", func(self objc.ID, _ objc.SEL, _ objc.ID) { shell.refreshMenu() }),
		m("windowWillClose:", func(self objc.ID, _ objc.SEL, _ objc.ID) {
			// Keep running in the menu bar; drop the Dock icon until the window comes back.
			shell.nsApp.Send(sel("setActivationPolicy:"), policyAccessory)
		}),
		// Links that open a new window (target=_blank) go to the default browser.
		m("webView:createWebViewWithConfiguration:forNavigationAction:windowFeatures:", func(self objc.ID, _ objc.SEL, _, _, action, _ objc.ID) objc.ID {
			if u := action.Send(sel("request")).Send(sel("URL")); u != 0 {
				cls("NSWorkspace").Send(sel("sharedWorkspace")).Send(sel("openURL:"), u)
			}
			return 0
		}),
		// The window only ever shows this copy's own server: any other page (a link, a redirect,
		// another program) is refused in-window and links the user clicked open in the browser.
		m("webView:decidePolicyForNavigationAction:decisionHandler:", func(self objc.ID, _ objc.SEL, _, action, handler objc.ID) {
			var u objc.ID
			if req := action.Send(sel("request")); req != 0 {
				u = req.Send(sel("URL"))
			}
			s := ""
			if u != 0 {
				s = gostr(u.Send(sel("absoluteString")))
			}
			if sameOrigin(s, shell.o.Origin) || s == "about:blank" {
				callBlock(handler, 1) // WKNavigationActionPolicyAllow
				return
			}
			log.Printf("window: refused navigation to %q (not %s)", s, shell.o.Origin)
			if u != 0 && objc.Send[int](action, sel("navigationType")) == 0 { // link activated
				cls("NSWorkspace").Send(sel("sharedWorkspace")).Send(sel("openURL:"), u)
			}
			callBlock(handler, 0) // WKNavigationActionPolicyCancel
		}),
		// Notifications: show banners even while the window is in front; clicking one opens the window.
		m("userNotificationCenter:willPresentNotification:withCompletionHandler:", func(self objc.ID, _ objc.SEL, _, _, handler objc.ID) {
			callBlock(handler, 2|8|16) // sound | list | banner
		}),
		m("userNotificationCenter:didReceiveNotificationResponse:withCompletionHandler:", func(self objc.ID, _ objc.SEL, _, _, handler objc.ID) {
			shell.show()
			callBlock(handler)
		}),
	})
	return err
}

func (s *macShell) item(title, action, key string, mods uint) objc.ID {
	var a objc.SEL
	if action != "" {
		a = sel(action)
	}
	it := cls("NSMenuItem").Send(sel("alloc")).Send(sel("initWithTitle:action:keyEquivalent:"), nsstr(title), a, nsstr(key))
	if mods != 0 {
		it.Send(sel("setKeyEquivalentModifierMask:"), mods)
	}
	return it
}

// own: item whose action is handled by our delegate (others go to the responder chain).
func (s *macShell) own(title, action, key string) objc.ID {
	it := s.item(title, action, key, 0)
	it.Send(sel("setTarget:"), s.delegate)
	return it
}

func menu(title string, items ...objc.ID) objc.ID {
	m := cls("NSMenu").Send(sel("alloc")).Send(sel("initWithTitle:"), nsstr(title))
	for _, it := range items {
		m.Send(sel("addItem:"), it)
	}
	return m
}
func sep() objc.ID { return cls("NSMenuItem").Send(sel("separatorItem")) }

func (s *macShell) buildMainMenu() {
	n := s.o.Name
	bar := menu("")
	add := func(title string, sub objc.ID) {
		it := s.item(title, "", "", 0)
		it.Send(sel("setSubmenu:"), sub)
		bar.Send(sel("addItem:"), it)
	}
	add(n, menu(n,
		s.own("About "+n, "showAbout:", ""),
		sep(),
		s.own("Settings…", "showSettings:", ","),
		sep(),
		s.item("Hide "+n, "hide:", "h", 0),
		s.item("Hide Others", "hideOtherApplications:", "h", cmdKey|optKey),
		s.item("Show All", "unhideAllApplications:", "", 0),
		sep(),
		s.item("Quit "+n, "terminate:", "q", 0),
	))
	add("Edit", menu("Edit",
		s.item("Undo", "undo:", "z", 0),
		s.item("Redo", "redo:", "Z", 0),
		sep(),
		s.item("Cut", "cut:", "x", 0),
		s.item("Copy", "copy:", "c", 0),
		s.item("Paste", "paste:", "v", 0),
		s.item("Select All", "selectAll:", "a", 0),
	))
	add("View", menu("View",
		s.own("Reload", "reloadPage:", "r"),
		sep(),
		s.item("Enter Full Screen", "toggleFullScreen:", "f", cmdKey|(1<<18)),
	))
	win := menu("Window",
		s.item("Minimize", "performMiniaturize:", "m", 0),
		s.item("Zoom", "performZoom:", "", 0),
		s.item("Close", "performClose:", "w", 0),
		sep(),
		s.own("Dashboard", "openDashboard:", "0"),
		s.item("Bring All to Front", "arrangeInFront:", "", 0),
	)
	add("Window", win)
	s.nsApp.Send(sel("setMainMenu:"), bar)
	s.nsApp.Send(sel("setWindowsMenu:"), win)
}

func (s *macShell) buildStatusItem() {
	s.status = cls("NSStatusBar").Send(sel("systemStatusBar")).Send(sel("statusItemWithLength:"), float64(-1))
	s.status.Send(sel("retain"))
	data := cls("NSData").Send(sel("dataWithBytes:length:"), unsafe.Pointer(&statusPNG[0]), uint(len(statusPNG)))
	img := cls("NSImage").Send(sel("alloc")).Send(sel("initWithData:"), data)
	img.Send(sel("setSize:"), nsSize{W: 18, H: 18})
	img.Send(sel("setTemplate:"), true)
	btn := s.status.Send(sel("button"))
	btn.Send(sel("setImage:"), img)
	btn.Send(sel("setToolTip:"), nsstr(s.o.Name))
	s.itHash = s.item("…", "", "", 0)
	s.itHash.Send(sel("setEnabled:"), false)
	s.itDetail = s.item("", "", "", 0)
	s.itDetail.Send(sel("setEnabled:"), false)
	s.itRemote = s.own("Remote access", "toggleRemote:", "")
	m := menu(s.o.Name,
		s.itHash, s.itDetail, sep(),
		s.own("Open Dashboard", "openDashboard:", ""),
		s.itRemote,
		sep(),
		s.own("Settings…", "showSettings:", ""),
		s.item("Quit "+s.o.Name, "terminate:", "", 0),
	)
	m.Send(sel("setAutoenablesItems:"), false)
	m.Send(sel("setDelegate:"), s.delegate)
	s.status.Send(sel("setMenu:"), m)
	s.refreshMenu()
}

func (s *macShell) refreshMenu() {
	st := s.o.Srv.Status()
	s.itHash.Send(sel("setTitle:"), nsstr(st.Hashrate))
	s.itDetail.Send(sel("setTitle:"), nsstr(st.Detail))
	s.itDetail.Send(sel("setHidden:"), st.Detail == "")
	s.itRemote.Send(sel("setHidden:"), !st.RemoteOK)
	state := 0
	if st.Remote {
		state = 1
	}
	s.itRemote.Send(sel("setState:"), state)
	t := "Remote access"
	if st.Remote && st.RemoteURL == "" {
		t = "Remote access (connecting…)"
	}
	s.itRemote.Send(sel("setTitle:"), nsstr(t))
}

func (s *macShell) toggleRemote() {
	st := s.o.Srv.Status()
	err := s.o.Srv.SetRemote(!st.Remote)
	if errors.Is(err, app.ErrNeedPassword) {
		s.show()
		s.js(`location.hash = "#settings?remote"`)
		return
	}
	if err != nil {
		log.Printf("remote access: %v", err)
	}
	s.refreshMenu()
}

func (s *macShell) buildWindow() {
	const titled, closable, mini, resizable = 1, 2, 4, 8
	s.win = cls("NSWindow").Send(sel("alloc")).Send(sel("initWithContentRect:styleMask:backing:defer:"),
		nsRect{Size: nsSize{W: 1280, H: 820}}, uint(titled|closable|mini|resizable), uint(2), false)
	s.win.Send(sel("setReleasedWhenClosed:"), false)
	s.win.Send(sel("setTitle:"), nsstr(s.o.Name))
	s.win.Send(sel("setTitlebarAppearsTransparent:"), true)
	s.win.Send(sel("setTitleVisibility:"), 1) // hidden: a slim dark title strip with the traffic lights
	s.win.Send(sel("setAppearance:"), cls("NSAppearance").Send(sel("appearanceNamed:"), nsstr("NSAppearanceNameDarkAqua")))
	s.win.Send(sel("setBackgroundColor:"), cls("NSColor").Send(sel("colorWithSRGBRed:green:blue:alpha:"), 8.0/255, 9.0/255, 11.0/255, 1.0))
	s.win.Send(sel("setMinSize:"), nsSize{W: 760, H: 520})
	s.win.Send(sel("setDelegate:"), s.delegate)
	if !objc.Send[bool](s.win, sel("setFrameUsingName:"), nsstr("HashWatchMain")) {
		s.win.Send(sel("center"))
	}
	s.win.Send(sel("setFrameAutosaveName:"), nsstr("HashWatchMain")) // size + position remembered by AppKit

	cfg := cls("WKWebViewConfiguration").Send(sel("new"))
	s.web = cls("WKWebView").Send(sel("alloc")).Send(sel("initWithFrame:configuration:"), nsRect{Size: nsSize{W: 1280, H: 820}}, cfg)
	s.web.Send(sel("setValue:forKey:"), cls("NSNumber").Send(sel("numberWithBool:"), false), nsstr("drawsBackground"))
	s.web.Send(sel("setUIDelegate:"), s.delegate)
	s.web.Send(sel("setNavigationDelegate:"), s.delegate)
	s.web.Send(sel("setAllowsBackForwardNavigationGestures:"), false)
	s.web.Send(sel("setAutoresizingMask:"), uint(18))
	s.win.Send(sel("setContentView:"), s.web)
	u := cls("NSURL").Send(sel("URLWithString:"), nsstr(s.o.URL))
	s.web.Send(sel("loadRequest:"), cls("NSURLRequest").Send(sel("requestWithURL:"), u))
}

func (s *macShell) show() {
	s.nsApp.Send(sel("setActivationPolicy:"), policyRegular)
	s.win.Send(sel("makeKeyAndOrderFront:"), objc.ID(0))
	s.nsApp.Send(sel("activateIgnoringOtherApps:"), true)
}

func (s *macShell) js(code string) {
	s.web.Send(sel("evaluateJavaScript:completionHandler:"), nsstr(code), objc.ID(0))
}

func (s *macShell) about() {
	s.show()
	credits := cls("NSAttributedString").Send(sel("alloc")).Send(sel("initWithString:"), nsstr(s.o.Tagline))
	opts := cls("NSDictionary").Send(sel("dictionaryWithObject:forKey:"), credits, nsstr("Credits"))
	s.nsApp.Send(sel("orderFrontStandardAboutPanelWithOptions:"), opts)
}

// Native notifications through UNUserNotificationCenter (falls back to osascript if unavailable).
func (s *macShell) setupNotifications() {
	if objc.GetClass("UNUserNotificationCenter") == 0 {
		return
	}
	center := cls("UNUserNotificationCenter").Send(sel("currentNotificationCenter"))
	if center == 0 {
		return
	}
	center.Send(sel("setDelegate:"), s.delegate)
	done := objc.NewBlock(func(_ objc.Block, granted bool, _ objc.ID) {
		log.Printf("notifications allowed: %v", granted)
	})
	center.Send(sel("requestAuthorizationWithOptions:completionHandler:"), uint(1|2|4), done) // badge|sound|alert
	var n uint64
	var mu sync.Mutex
	notify.Native = func(title, body string) error {
		mu.Lock()
		n++
		id := fmt.Sprintf("hashwatch-%d", n)
		mu.Unlock()
		pool := cls("NSAutoreleasePool").Send(sel("new"))
		defer pool.Send(sel("drain"))
		c := cls("UNMutableNotificationContent").Send(sel("new"))
		c.Send(sel("setTitle:"), nsstr(title))
		c.Send(sel("setBody:"), nsstr(body))
		c.Send(sel("setSound:"), cls("UNNotificationSound").Send(sel("defaultSound")))
		req := cls("UNNotificationRequest").Send(sel("requestWithIdentifier:content:trigger:"), nsstr(id), c, objc.ID(0))
		center.Send(sel("addNotificationRequest:withCompletionHandler:"), req, objc.ID(0))
		c.Send(sel("release"))
		return nil
	}
}
