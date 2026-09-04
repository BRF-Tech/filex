; "Open with filex" — the Windows half of the file-type registration.
;
; ⚠⚠ Written by hand instead of using electron-builder's `fileAssociations`,
; and that is the whole point of this file. Its `APP_ASSOCIATE` macro does:
;
;     WriteRegStr SHELL_CONTEXT "Software\Classes\.docx" "" "<our ProgId>"
;
; — it overwrites the DEFAULT ProgId of `.docx`. That is taking the file type,
; at install time, without anybody being asked. On a machine where nothing else
; has claimed `.docx` (a fresh Windows with no Office — exactly the machine this
; feature exists for) that is enough to make filex the handler for every Word
; document on it. electron-builder's own docs also say the macro "works only if
; nsis.perMachine is true", and this installer is deliberately per-user.
;
; What is written here instead is the documented, ADDITIVE registration:
;
;   • a ProgId of our own (`filex.OfficeDocument`) with an open verb,
;   • `Applications\filex.exe` with a friendly name and a SupportedTypes list —
;     this is what puts filex in the "Open with" list at all,
;   • one `OpenWithProgids` value per extension, which ADDS filex to that
;     extension's candidate list and touches nothing already there.
;
; None of it changes what opens a document today. Becoming the DEFAULT is the
; user's act, in Windows' own Default apps page: since Windows 10 the
; `…\FileExts\.docx\UserChoice` key carries a hash over the extension, the
; user's SID and a Microsoft salt, and a value written without a valid hash is
; ignored. Forging it is precisely what that protection exists to stop, so the
; app does not try — Settings opens `ms-settings:defaultapps` and the user is
; one click from finishing.
;
; SHELL_CONTEXT is HKEY_CURRENT_USER for this installer (perMachine: false,
; allowElevation: false in electron-builder.yml), so everything below is written
; and removed under the installing user's own hive.

!define FILEX_PROGID "filex.OfficeDocument"

; One extension, added to filex's candidate list and to filex's own supported
; types. Both halves are needed: OpenWithProgids is what Explorer's "Open with"
; submenu reads, SupportedTypes is what the "Choose another app" dialog reads.
!macro filexClaimExt EXT
  WriteRegStr SHELL_CONTEXT "Software\Classes\${EXT}\OpenWithProgids" "${FILEX_PROGID}" ""
  WriteRegStr SHELL_CONTEXT "Software\Classes\Applications\${APP_EXECUTABLE_FILENAME}\SupportedTypes" "${EXT}" ""
!macroend

!macro filexReleaseExt EXT
  DeleteRegValue SHELL_CONTEXT "Software\Classes\${EXT}\OpenWithProgids" "${FILEX_PROGID}"
!macroend

; ⚠ The two lists below are written out rather than looped through a macro
; name passed as a parameter: NSIS' preprocessor is not a language to be clever
; in, and this file cannot be unit-tested — it is only ever exercised by a real
; installer run. The office types mirror OFFICE_EXTENSIONS in
; desktop/src/openwith.ts; widening one list without the other gives an app that
; offers to open a type it then refuses.

!macro customInstall
  WriteRegStr SHELL_CONTEXT "Software\Classes\${FILEX_PROGID}" "" "Office document"
  WriteRegStr SHELL_CONTEXT "Software\Classes\${FILEX_PROGID}\DefaultIcon" "" "$INSTDIR\${APP_EXECUTABLE_FILENAME},0"
  ; ⚠ The quotes around %1 are not optional. Without them a document in
  ; "C:\Users\Ada Lovelace\Belgeler\rapor.docx" reaches the app as four
  ; arguments, none of which is a file — the failure looks like "the app opened
  ; and did nothing" and only ever happens to people whose paths have spaces.
  WriteRegStr SHELL_CONTEXT "Software\Classes\${FILEX_PROGID}\shell\open\command" "" '"$INSTDIR\${APP_EXECUTABLE_FILENAME}" "%1"'

  WriteRegStr SHELL_CONTEXT "Software\Classes\Applications\${APP_EXECUTABLE_FILENAME}" "FriendlyAppName" "${PRODUCT_NAME}"
  WriteRegStr SHELL_CONTEXT "Software\Classes\Applications\${APP_EXECUTABLE_FILENAME}\shell\open\command" "" '"$INSTDIR\${APP_EXECUTABLE_FILENAME}" "%1"'

  !insertmacro filexClaimExt ".docx"
  !insertmacro filexClaimExt ".doc"
  !insertmacro filexClaimExt ".xlsx"
  !insertmacro filexClaimExt ".xls"
  !insertmacro filexClaimExt ".pptx"
  !insertmacro filexClaimExt ".ppt"
  !insertmacro filexClaimExt ".odt"
  !insertmacro filexClaimExt ".ods"
  !insertmacro filexClaimExt ".odp"
  !insertmacro filexClaimExt ".rtf"

  ; Tell Explorer to re-read associations. Without it the new entry does not
  ; appear in "Open with" until the next sign-in, which reads as "the installer
  ; did nothing".
  System::Call 'shell32::SHChangeNotify(i 0x08000000, i 0, i 0, i 0)'
!macroend

!macro customUnInstall
  !insertmacro filexReleaseExt ".docx"
  !insertmacro filexReleaseExt ".doc"
  !insertmacro filexReleaseExt ".xlsx"
  !insertmacro filexReleaseExt ".xls"
  !insertmacro filexReleaseExt ".pptx"
  !insertmacro filexReleaseExt ".ppt"
  !insertmacro filexReleaseExt ".odt"
  !insertmacro filexReleaseExt ".ods"
  !insertmacro filexReleaseExt ".odp"
  !insertmacro filexReleaseExt ".rtf"
  DeleteRegKey SHELL_CONTEXT "Software\Classes\${FILEX_PROGID}"
  DeleteRegKey SHELL_CONTEXT "Software\Classes\Applications\${APP_EXECUTABLE_FILENAME}"
  System::Call 'shell32::SHChangeNotify(i 0x08000000, i 0, i 0, i 0)'
!macroend
