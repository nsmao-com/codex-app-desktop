Unicode true

####
## Please note: Template replacements don't work in this file. They are provided with default defines like
## mentioned underneath.
## If the keyword is not defined, "wails_tools.nsh" will populate them.
## If they are defined here, "wails_tools.nsh" will not touch them. This allows you to use this project.nsi manually
## from outside of Wails for debugging and development of the installer.
## 
## For development first make a wails nsis build to populate the "wails_tools.nsh":
## > wails build --target windows/amd64 --nsis
## Then you can call makensis on this file with specifying the path to your binary:
## For a AMD64 only installer:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app.exe
## For a ARM64 only installer:
## > makensis -DARG_WAILS_ARM64_BINARY=..\..\bin\app.exe
## For a installer with both architectures:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app-amd64.exe -DARG_WAILS_ARM64_BINARY=..\..\bin\app-arm64.exe
####
## The following information is taken from the wails_tools.nsh file, but they can be overwritten here.
####
## !define INFO_PROJECTNAME    "my-project" # Default "NiceCodex"
## !define INFO_COMPANYNAME    "My Company" # Default "Nice Codex"
## !define INFO_PRODUCTNAME    "My Product Name" # Default "Nice Codex"
## !define INFO_PRODUCTVERSION "1.0.0"     # Default "0.1.0"
## !define INFO_COPYRIGHT      "(c) Now, My Company" # Default "Copyright 2026 Nice Codex"
###
## !define PRODUCT_EXECUTABLE  "Application.exe"      # Default "${INFO_PROJECTNAME}.exe"
## !define UNINST_KEY_NAME     "UninstKeyInRegistry"  # Default "${INFO_COMPANYNAME}${INFO_PRODUCTNAME}"
####
## !define REQUEST_EXECUTION_LEVEL "admin"            # Default "admin"  see also https://nsis.sourceforge.io/Docs/Chapter4.html
## !define WAILS_INSTALL_SCOPE     "user"             # Default "machine" - set to "user" for per-user install ($LOCALAPPDATA) without UAC prompt
####
## Include the wails tools
####
!include "wails_tools.nsh"

# The version information for this two must consist of 4 parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support. https://nsis.sourceforge.io/Reference/ManifestDPIAware
ManifestDPIAware true

!include "MUI.nsh"
!include "FileFunc.nsh"
!include "Sections.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
# !define MUI_WELCOMEFINISHPAGE_BITMAP "resources\leftimage.bmp" #Include this to add a bitmap on the left side of the Welcome Page. Must be a size of 164x314
!define MUI_FINISHPAGE_NOAUTOCLOSE # Wait on the INSTFILES page so the user can take a look into the details of the installation steps
!define MUI_ABORTWARNING # This will warn the user if they exit from the installer.

!insertmacro MUI_PAGE_WELCOME # Welcome to the installer page.
# !insertmacro MUI_PAGE_LICENSE "resources\eula.txt" # Adds a EULA page to the installer
!insertmacro MUI_PAGE_DIRECTORY # In which folder install page.
!define MUI_PAGE_HEADER_TEXT "Choose Shortcuts"
!define MUI_PAGE_HEADER_SUBTEXT "Choose where ${INFO_PRODUCTNAME} appears in Windows."
!define MUI_COMPONENTSPAGE_TEXT_TOP "Choose which shortcuts to keep. Your choices will be remembered for future updates. Unchecked shortcuts will be removed."
!define MUI_COMPONENTSPAGE_TEXT_COMPLIST "Shortcuts:"
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_INSTFILES # Installing page.
!insertmacro MUI_PAGE_FINISH # Finished installation page.

!insertmacro MUI_UNPAGE_INSTFILES # Uninstalling page

!insertmacro MUI_LANGUAGE "English" # Set the Language of the installer

## The following two statements can be used to sign the installer and the uninstaller. The path to the binaries are provided in %1
#!uninstfinalize 'signtool --file "%1"'
#!finalize 'signtool --file "%1"'

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe" # Name of the installer's file.
# Resolve defaults in .onInit so an explicit NSIS /D= path always wins.
InstallDir ""
ShowInstDetails show # This will always show the installation details.

Section
    !insertmacro wails.setShellContext

    !insertmacro wails.webview2runtime

    SetOutPath $INSTDIR
    
    !insertmacro wails.files

    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols
    
    !insertmacro wails.writeUninstaller
    WriteRegStr SHCTX "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
SectionEnd

Section "Desktop shortcut" DesktopShortcut
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
SectionEnd

Section "Start menu shortcut" StartMenuShortcut
    CreateShortCut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
SectionEnd

Section -SaveShortcutChoices
    ${If} ${SectionIsSelected} ${DesktopShortcut}
        WriteRegDWORD SHCTX "${UNINST_KEY}" "DesktopShortcut" 1
    ${Else}
        Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"
        WriteRegDWORD SHCTX "${UNINST_KEY}" "DesktopShortcut" 0
    ${EndIf}
    ${If} ${SectionIsSelected} ${StartMenuShortcut}
        WriteRegDWORD SHCTX "${UNINST_KEY}" "StartMenuShortcut" 1
    ${Else}
        Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
        WriteRegDWORD SHCTX "${UNINST_KEY}" "StartMenuShortcut" 0
    ${EndIf}
SectionEnd

Function .onInit
    !insertmacro wails.checkArchitecture
    !insertmacro wails.setShellContext
    SetRegView 64

    ReadRegStr $0 SHCTX "${UNINST_KEY}" "InstallLocation"
    ${If} $0 == ""
        # Older installers only stored the executable path, without an icon index.
        ReadRegStr $1 SHCTX "${UNINST_KEY}" "DisplayIcon"
        ${If} $1 != ""
            ${GetParent} "$1" $0
        ${EndIf}
    ${EndIf}
    ${If} $INSTDIR == ""
        ${If} $0 != ""
            StrCpy $INSTDIR $0
        ${Else}
            !if "${WAILS_INSTALL_SCOPE}" == "user"
                StrCpy $INSTDIR "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
            !else
                StrCpy $INSTDIR "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
            !endif
        ${EndIf}
    ${EndIf}

    # Migrate old installs using the actual links, including links users removed.
    ClearErrors
    ReadRegDWORD $1 SHCTX "${UNINST_KEY}" "DesktopShortcut"
    ${If} ${Errors}
        ${If} $0 != ""
            ${IfNot} ${FileExists} "$DESKTOP\${INFO_PRODUCTNAME}.lnk"
                !insertmacro UnselectSection ${DesktopShortcut}
            ${EndIf}
        ${EndIf}
    ${ElseIf} $1 == 0
        !insertmacro UnselectSection ${DesktopShortcut}
    ${EndIf}
    ClearErrors
    ReadRegDWORD $1 SHCTX "${UNINST_KEY}" "StartMenuShortcut"
    ${If} ${Errors}
        ${If} $0 != ""
            ${IfNot} ${FileExists} "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
                !insertmacro UnselectSection ${StartMenuShortcut}
            ${EndIf}
        ${EndIf}
    ${ElseIf} $1 == 0
        !insertmacro UnselectSection ${StartMenuShortcut}
    ${EndIf}
FunctionEnd

!insertmacro MUI_FUNCTION_DESCRIPTION_BEGIN
    !insertmacro MUI_DESCRIPTION_TEXT ${DesktopShortcut} "Show ${INFO_PRODUCTNAME} on the desktop."
    !insertmacro MUI_DESCRIPTION_TEXT ${StartMenuShortcut} "Show ${INFO_PRODUCTNAME} in the Windows Start menu."
!insertmacro MUI_FUNCTION_DESCRIPTION_END

Section "uninstall" 
    !insertmacro wails.setShellContext

    RMDir /r "$AppData\${PRODUCT_EXECUTABLE}" # Remove the WebView2 DataPath

    RMDir /r $INSTDIR

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    !insertmacro wails.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols

    !insertmacro wails.deleteUninstaller
SectionEnd
