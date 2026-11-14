# Testing HashWatch on your Mac

Thanks for testing! This build has **everything switched on**: controls, alerts,
remote access, auto-start and multiple miners. Nothing is read-only.

## 1. Install
1. Open `HashWatch.dmg`, drag **HashWatch** onto **Applications**, eject the disk image.
2. Open HashWatch from Applications.
3. "Apple could not verify…": in Finder, Control-click (or right-click) HashWatch,
   choose **Open**, then **Open** again. Or use **System Settings → Privacy &
   Security → Open Anyway** (once) and confirm with your password or Touch ID.
4. "Find devices on local networks?": click **Allow**. Allow notifications too.

## 2. Set up your miner
- The setup screen scans your network for Avalon miners. Pick yours, **or** type
  its IP address (for example `192.168.1.50`; port 4028 is the default).
- Choose a password when asked. It protects the dashboard on your phone and
  remote access.

## 3. What to test
| Area | What to try | What should happen |
|---|---|---|
| Window | Resize, move, quit and reopen | Size and position are remembered. Minimum size is enforced. |
| Menu bar | Click the hexagon icon at the top right | It shows the live hashrate (and temperature), Open Dashboard, Remote access and Quit. |
| Close | Close the window (red button or Cmd-W) | HashWatch keeps running in the menu bar, and the Dock icon goes away. |
| Quit | Cmd-Q, or menu bar → Quit | Everything stops, including remote access. |
| Live data | Leave it open for a few minutes | It updates every 5 seconds, with hashrate, temperatures and fans matching the miner's own page. |
| Controls | **Controls** → change the work speed, toggle the light | A confirm dialog appears and the miner changes. Change it back afterwards. |
| Alerts | Unplug the miner's network cable for about 2 minutes | You get a Mac notification that it's offline, then another when it's back. |
| Remote access | Settings → Remote access → On, and open the link on your phone (with Wi-Fi off) | The dashboard loads after you enter your password. |
| iPhone | Open the phone link in Safari → Share → Add to Home Screen | It works like an app. |
| Auto-start | Settings → **Start automatically** → On, then log out and back in | HashWatch starts quietly in the menu bar. |
| More miners | Add a second miner by IP, if you have one | Both show up. |
| Move to Applications | Open it straight from the DMG once | It offers to move itself to Applications. |

## 4. Sending us debug info
**Settings → Copy debug info**, then paste it into a message. It has the app and
macOS versions, the miner's raw API replies (summary, stats, estats, pools) and
recent errors. Passwords are redacted. If something looks off, a screenshot helps too.
