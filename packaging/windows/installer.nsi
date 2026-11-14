; HashWatch installer (NSIS 3, built on Linux with makensis).
; makensis -DVERSION=v0.5.0 -DNUMVER=0.5.0.0 -DSRC=<dir with HashWatch.exe> -DOUT=<setup.exe> installer.nsi
Unicode true
!include "MUI2.nsh"
!include "x64.nsh"
!include "FileFunc.nsh"

!define NAME "HashWatch"
!define TAGLINE "Powered by GrumpTech"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\HashWatch"

Name "${NAME}"
Caption "${NAME} ${VERSION} Setup"
BrandingText "${NAME} · ${TAGLINE}"
OutFile "${OUT}"
InstallDir "$PROGRAMFILES64\${NAME}"
InstallDirRegKey HKLM "Software\${NAME}" "InstallDir"
RequestExecutionLevel admin
SetCompressor /SOLID lzma
ManifestDPIAware true

VIProductVersion "${NUMVER}"
VIAddVersionKey "ProductName" "${NAME}"
VIAddVersionKey "CompanyName" "GrumpTech"
VIAddVersionKey "FileDescription" "${NAME} Setup"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "${TAGLINE}"

!define MUI_ICON "HashWatch.ico"
!define MUI_UNICON "HashWatch.ico"
!define MUI_ABORTWARNING
!define MUI_WELCOMEPAGE_TITLE "Install ${NAME}"
!define MUI_WELCOMEPAGE_TEXT "${NAME} watches your Avalon miners: hashrate, temperature, alerts and remote access from your phone.$\r$\n$\r$\n${TAGLINE}"
!define MUI_FINISHPAGE_RUN
!define MUI_FINISHPAGE_RUN_TEXT "Open ${NAME} now"
!define MUI_FINISHPAGE_RUN_FUNCTION LaunchApp

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Function .onInit
  ${IfNot} ${RunningX64}
    MessageBox MB_ICONSTOP "${NAME} needs 64-bit Windows 10 or 11."
    Abort
  ${EndIf}
  SetRegView 64
FunctionEnd

Function LaunchApp
  ; Start it as the signed-in user, not elevated.
  Exec '"$WINDIR\explorer.exe" "$INSTDIR\HashWatch.exe"'
FunctionEnd

Section "${NAME} (required)" SecMain
  SectionIn RO
  ; Stop a running copy so its files can be replaced (settings are kept).
  nsExec::Exec 'taskkill /IM HashWatch.exe /F'
  Sleep 500
  SetOutPath "$INSTDIR"
  File "${SRC}\HashWatch.exe"
  File "${SRC}\Read me first.txt"
  WriteUninstaller "$INSTDIR\Uninstall ${NAME}.exe"
  CreateShortcut "$SMPROGRAMS\${NAME}.lnk" "$INSTDIR\HashWatch.exe" "" "$INSTDIR\HashWatch.exe" 0
  WriteRegStr HKLM "Software\${NAME}" "InstallDir" "$INSTDIR"
  WriteRegStr HKLM "${UNINST_KEY}" "DisplayName" "${NAME}"
  WriteRegStr HKLM "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "${UNINST_KEY}" "Publisher" "GrumpTech"
  WriteRegStr HKLM "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\HashWatch.exe,0"
  WriteRegStr HKLM "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "${UNINST_KEY}" "UninstallString" '"$INSTDIR\Uninstall ${NAME}.exe"'
  WriteRegStr HKLM "${UNINST_KEY}" "QuietUninstallString" '"$INSTDIR\Uninstall ${NAME}.exe" /S'
  WriteRegDWORD HKLM "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKLM "${UNINST_KEY}" "NoRepair" 1
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  WriteRegDWORD HKLM "${UNINST_KEY}" "EstimatedSize" "$0"
SectionEnd

Section "Desktop shortcut" SecDesktop
  CreateShortcut "$DESKTOP\${NAME}.lnk" "$INSTDIR\HashWatch.exe" "" "$INSTDIR\HashWatch.exe" 0
SectionEnd

!insertmacro MUI_FUNCTION_DESCRIPTION_BEGIN
  !insertmacro MUI_DESCRIPTION_TEXT ${SecMain} "The ${NAME} app and a Start menu shortcut."
  !insertmacro MUI_DESCRIPTION_TEXT ${SecDesktop} "A shortcut on your desktop."
!insertmacro MUI_FUNCTION_DESCRIPTION_END

Function un.onInit
  SetRegView 64
FunctionEnd

Section "Uninstall"
  nsExec::Exec 'taskkill /IM HashWatch.exe /F'
  Sleep 500
  Delete "$INSTDIR\HashWatch.exe"
  Delete "$INSTDIR\Read me first.txt"
  Delete "$INSTDIR\Uninstall ${NAME}.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\${NAME}.lnk"
  Delete "$DESKTOP\${NAME}.lnk"
  ; Auto-start entry for the user running the uninstaller (settings in %APPDATA%\Hashwatch are kept).
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "Hashwatch"
  DeleteRegKey HKLM "${UNINST_KEY}"
  DeleteRegKey HKLM "Software\${NAME}"
SectionEnd
