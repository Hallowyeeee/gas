; 瓦斯行管理系統 安裝檔（Inno Setup）
; GitHub Actions 會自動編譯，產生 gas-setup.exe

#ifndef AppVersion
  #define AppVersion "dev"
#endif

[Setup]
AppId={{6C1F3B2A-9D4E-4F1B-8A51-7E2B0C9D3A11}
AppName=瓦斯行管理系統
AppVersion={#AppVersion}
AppPublisher=新福春進瓦斯聯合配送
DefaultDirName=C:\瓦斯行
DisableProgramGroupPage=yes
DisableDirPage=auto
UsePreviousAppDir=yes
PrivilegesRequired=lowest
OutputDir=..\dist
OutputBaseFilename=gas-setup
SetupIconFile=..\cmd\gas\icon.ico
UninstallDisplayIcon={app}\gas.exe
UninstallDisplayName=瓦斯行管理系統
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
CloseApplications=force
RestartApplications=no
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible

[Languages]
#if FileExists(AddBackslash(SourcePath) + "ChineseTraditional.isl")
Name: "zh"; MessagesFile: "ChineseTraditional.isl"
#elif FileExists(AddBackslash(CompilerPath) + "Languages\ChineseTraditional.isl")
Name: "zh"; MessagesFile: "compiler:Languages\ChineseTraditional.isl"
#else
Name: "en"; MessagesFile: "compiler:Default.isl"
#endif

[CustomMessages]
DesktopIcon=在桌面建立捷徑
AutoStart=開機自動啟動（建議，師傅才隨時收得到單）
Launch=現在打開瓦斯行管理系統
StationGroup=這台電腦是：
StationMain=主電腦（資料放這台、接來電盒，要一直開著）
StationClient=分機（連到店裡的主電腦，自己不放資料）
Firewall=讓店裡其他電腦連進來（Windows 會問一次「是否允許」，請按「是」）

[Tasks]
Name: "mainpc"; Description: "{cm:StationMain}"; GroupDescription: "{cm:StationGroup}"; Flags: exclusive
Name: "mainpc\firewall"; Description: "{cm:Firewall}"
Name: "clientpc"; Description: "{cm:StationClient}"; GroupDescription: "{cm:StationGroup}"; Flags: exclusive unchecked
Name: "desktopicon"; Description: "{cm:DesktopIcon}"
Name: "autostart"; Description: "{cm:AutoStart}"

[Files]
Source: "..\dist\gas.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\dist\cloudflared.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\dist\guide.md"; DestDir: "{app}"; DestName: "試跑教學.md"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\瓦斯行管理系統"; Filename: "{app}\gas.exe"; WorkingDir: "{app}"
Name: "{autodesktop}\瓦斯行管理系統"; Filename: "{app}\gas.exe"; WorkingDir: "{app}"; Tasks: desktopicon

[Registry]
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "GasShop"; ValueData: """{app}\gas.exe"" -tray"; Flags: uninsdeletevalue; Tasks: autostart

[Run]
Filename: "{app}\gas.exe"; Parameters: "-station main"; WorkingDir: "{app}"; Flags: runhidden waituntilterminated; Tasks: mainpc
Filename: "{app}\gas.exe"; Parameters: "-station client"; WorkingDir: "{app}"; Flags: runhidden waituntilterminated; Tasks: clientpc
Filename: "{sys}\cmd.exe"; Parameters: "/c netsh advfirewall firewall delete rule name=GasShop & netsh advfirewall firewall add rule name=GasShop dir=in action=allow protocol=TCP localport=8080 remoteip=localsubnet profile=any"; Verb: "runas"; Flags: shellexec runhidden waituntilterminated; Tasks: mainpc\firewall
Filename: "{app}\gas.exe"; Description: "{cm:Launch}"; WorkingDir: "{app}"; Flags: postinstall nowait skipifsilent

[UninstallRun]
Filename: "{sys}\taskkill.exe"; Parameters: "/f /im gas.exe"; Flags: runhidden; RunOnceId: "KillGas"
Filename: "{sys}\taskkill.exe"; Parameters: "/f /im cloudflared.exe"; Flags: runhidden; RunOnceId: "KillTunnel"

; 解除安裝時，資料庫 gas.db、備份 backups、紀錄 gas.log 都會保留，不會被刪掉。
