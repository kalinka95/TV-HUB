//go:build windows

// Bluetooth и Wi-Fi через встроенные средства Windows (WinRT из PowerShell):
// ничего устанавливать не нужно.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
)

// Общее начало сценариев: ожидание асинхронных операций WinRT и нужные типы.
// {BT} — обратная кавычка (в строке Go её не записать).
const connPrelude = `
$ErrorActionPreference = 'SilentlyContinue'
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$asOp = ([System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object { $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation{BT}1' })[0]
$asAct = ([System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object { $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncAction' })[0]
function Await($op, [Type]$t, $ms = 15000) { if (-not $op) { return $null }; $task = $asOp.MakeGenericMethod($t).Invoke($null, @($op)); $null = $task.Wait($ms); if ($task.IsCompleted -and -not $task.IsFaulted) { $task.Result } else { $null } }
function AwaitAct($op, $ms = 15000) { if ($op) { $task = $asAct.Invoke($null, @($op)); $null = $task.Wait($ms) } }
$null = [Windows.Devices.Enumeration.DeviceInformation, Windows.Devices.Enumeration, ContentType = WindowsRuntime]
$null = [Windows.Devices.Bluetooth.BluetoothDevice, Windows.Devices.Bluetooth, ContentType = WindowsRuntime]
$null = [Windows.Devices.Radios.Radio, Windows.System.Devices, ContentType = WindowsRuntime]
$null = [Windows.Devices.WiFi.WiFiAdapter, Windows.System.Devices, ContentType = WindowsRuntime]
$null = [Windows.Networking.Connectivity.NetworkInformation, Windows.Networking.Connectivity, ContentType = WindowsRuntime]
$null = [Windows.Security.Credentials.PasswordCredential, Windows.Security.Credentials, ContentType = WindowsRuntime]
function Emit($o) { [Console]::Out.WriteLine(($o | ConvertTo-Json -Compress -Depth 5)) }
$aepKind = [Windows.Devices.Enumeration.DeviceInformationKind]::AssociationEndpoint
$props = New-Object 'System.Collections.Generic.List[string]'
'System.Devices.Aep.IsPaired', 'System.Devices.Aep.IsConnected' | ForEach-Object { $props.Add($_) }
$btProto = 'System.Devices.Aep.ProtocolId:="{e0cbf06c-cd8b-4647-bb8a-263b43f0f974}"'
function BtRadio { $null = Await ([Windows.Devices.Radios.Radio]::RequestAccessAsync()) ([Windows.Devices.Radios.RadioAccessStatus]); $rs = Await ([Windows.Devices.Radios.Radio]::GetRadiosAsync()) ([System.Collections.Generic.IReadOnlyList[Windows.Devices.Radios.Radio]]); $rs | Where-Object { [string]$_.Kind -eq 'Bluetooth' } | Select-Object -First 1 }
function BtList($aqs, $ms) {
  $out = @()
  $list = Await ([Windows.Devices.Enumeration.DeviceInformation]::FindAllAsync($aqs, $props, $aepKind)) ([Windows.Devices.Enumeration.DeviceInformationCollection]) $ms
  foreach ($d in $list) {
    if (-not $d.Name) { continue }
    $cls = ''
    $bd = Await ([Windows.Devices.Bluetooth.BluetoothDevice]::FromIdAsync($d.Id)) ([Windows.Devices.Bluetooth.BluetoothDevice]) 3000
    if ($bd) { $cls = [string]$bd.ClassOfDevice.MajorClass + '/' + [string]$bd.ClassOfDevice.MinorClass; $bd.Dispose() }
    $out += @{ id = $d.Id; name = $d.Name; cls = $cls; paired = [bool]$d.Properties['System.Devices.Aep.IsPaired']; connected = [bool]$d.Properties['System.Devices.Aep.IsConnected'] }
  }
  , $out
}
# Поиск устройств — как в настройках Windows: «слушаем» эфир несколько секунд (DeviceWatcher),
# и обычный Bluetooth, и Bluetooth LE. Имена часто приходят позже — ловим и обновления.
function BtScan($secs) {
  $aqs = '(System.Devices.Aep.ProtocolId:="{e0cbf06c-cd8b-4647-bb8a-263b43f0f974}" OR System.Devices.Aep.ProtocolId:="{bb7bb05e-5972-42b5-94fc-76eaa7084d49}")'
  $w = [Windows.Devices.Enumeration.DeviceInformation]::CreateWatcher($aqs, $props, $aepKind)
  $seen = @{}; $n = 0
  $null = Register-ObjectEvent -InputObject $w -EventName Added -SourceIdentifier tvAdd
  $null = Register-ObjectEvent -InputObject $w -EventName Updated -SourceIdentifier tvUpd
  $w.Start()
  $end = (Get-Date).AddSeconds($secs)
  while ((Get-Date) -lt $end) {
    $e = Wait-Event -Timeout 1
    if (-not $e) { continue }
    $x = $e.SourceEventArgs
    if ($e.SourceIdentifier -eq 'tvAdd' -and $x) { $seen[$x.Id] = $x; $n++ }
    elseif ($e.SourceIdentifier -eq 'tvUpd' -and $x -and $seen.ContainsKey($x.Id)) { $seen[$x.Id].Update($x) }
    Remove-Event -EventIdentifier $e.EventIdentifier
  }
  $w.Stop()
  Unregister-Event -SourceIdentifier tvAdd; Unregister-Event -SourceIdentifier tvUpd
  # Запасной путь, если события не пришли: разовый запрос
  if ($n -eq 0) {
    $list = Await ([Windows.Devices.Enumeration.DeviceInformation]::FindAllAsync($aqs, $props, $aepKind)) ([Windows.Devices.Enumeration.DeviceInformationCollection]) 20000
    foreach ($d in $list) { $seen[$d.Id] = $d }
  }
  $out = @(); $names = @{}
  # Сначала обычный Bluetooth (наушники для музыки работают через него), потом LE с теми же именами не дублируем
  foreach ($d in ($seen.Values | Sort-Object { $_.Id -like 'BluetoothLE*' })) {
    $name = [string]$d.Name
    if (-not $name -or $names.ContainsKey($name)) { continue }
    $names[$name] = 1
    $cls = ''
    if ($d.Id -notlike 'BluetoothLE*') {
      $bd = Await ([Windows.Devices.Bluetooth.BluetoothDevice]::FromIdAsync($d.Id)) ([Windows.Devices.Bluetooth.BluetoothDevice]) 3000
      if ($bd) { $cls = [string]$bd.ClassOfDevice.MajorClass + '/' + [string]$bd.ClassOfDevice.MinorClass; $bd.Dispose() }
    }
    $out += @{ id = $d.Id; name = $name; cls = $cls; paired = [bool]$d.Properties['System.Devices.Aep.IsPaired']; connected = [bool]$d.Properties['System.Devices.Aep.IsConnected'] }
  }
  @{ devices = $out; events = $n; total = $seen.Count }
}
function Wifi {
  $null = Await ([Windows.Devices.WiFi.WiFiAdapter]::RequestAccessAsync()) ([Windows.Devices.WiFi.WiFiAccessStatus])
  $ads = Await ([Windows.Devices.WiFi.WiFiAdapter]::FindAllAdaptersAsync()) ([System.Collections.Generic.IReadOnlyList[Windows.Devices.WiFi.WiFiAdapter]])
  $ads | Select-Object -First 1
}
function WifiCurrent($a) {
  $p = Await ($a.NetworkAdapter.GetConnectedProfileAsync()) ([Windows.Networking.Connectivity.ConnectionProfile])
  if ($p -and $p.IsWlanConnectionProfile) { @{ ssid = [string]$p.WlanConnectionProfileDetails.GetConnectedSsid(); bars = [int]$p.GetSignalBars() } } else { $null }
}
`

// Состояние для значков вверху: Bluetooth (сопряжённые устройства) и текущая сеть Wi-Fi.
const connStatusScript = `
$r = BtRadio
$bt = @{ radio = $(if ($r) { [string]$r.State } else { 'none' }); devices = @() }
if ($r -and [string]$r.State -eq 'On') { $bt.devices = BtList ($btProto + ' AND System.Devices.Aep.IsPaired:=System.StructuredQueryType.Boolean#True') 8000 }
$a = Wifi
$wf = @{ adapter = [bool]$a; current = $null }
if ($a) { $wf.current = WifiCurrent $a }
Emit @{ bt = $bt; wifi = $wf }
`

const btScript = `
$act = $env:TVHUB_ACT; $id = $env:TVHUB_ID
switch ($act) {
  'scan' {
    Emit (BtScan 15)
  }
  'radio' {
    $r = BtRadio
    if (-not $r) { Emit @{ error = 'noradio' }; break }
    $st = if ($env:TVHUB_ON -eq '1') { [Windows.Devices.Radios.RadioState]::On } else { [Windows.Devices.Radios.RadioState]::Off }
    $res = Await ($r.SetStateAsync($st)) ([Windows.Devices.Radios.RadioAccessStatus])
    Emit @{ status = [string]$res }
  }
  'pair' {
    $d = Await ([Windows.Devices.Enumeration.DeviceInformation]::CreateFromIdAsync($id, $props, $aepKind)) ([Windows.Devices.Enumeration.DeviceInformation])
    if (-not $d) { Emit @{ error = 'notfound' }; break }
    $res = Await ($d.Pairing.PairAsync()) ([Windows.Devices.Enumeration.DevicePairingResult]) 60000
    Emit @{ status = [string]$res.Status }
  }
  'unpair' {
    $d = Await ([Windows.Devices.Enumeration.DeviceInformation]::CreateFromIdAsync($id, $props, $aepKind)) ([Windows.Devices.Enumeration.DeviceInformation])
    if (-not $d) { Emit @{ error = 'notfound' }; break }
    $res = Await ($d.Pairing.UnpairAsync()) ([Windows.Devices.Enumeration.DeviceUnpairingResult]) 30000
    Emit @{ status = [string]$res.Status }
  }
  'connect' {
    # Подключить сопряжённые наушники или колонку: перезапускаем у них службы звука
    # (A2DP и гарнитуру) — Windows сразу соединяется с устройством, если оно включено
    $bd = Await ([Windows.Devices.Bluetooth.BluetoothDevice]::FromIdAsync($id)) ([Windows.Devices.Bluetooth.BluetoothDevice]) 5000
    if (-not $bd) { Emit @{ error = 'notfound' }; break }
    $addr = $bd.BluetoothAddress; $bd.Dispose()
    Add-Type -TypeDefinition @'
using System; using System.Runtime.InteropServices;
public static class TvhubBt {
  [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
  public struct Info { public int Size; public ulong Address; public uint Class; public int Connected; public int Remembered; public int Authenticated; public long Seen1; public long Seen2; public long Used1; public long Used2; [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 248)] public string Name; }
  [StructLayout(LayoutKind.Sequential)] struct FindParams { public int Size; }
  [DllImport("bthprops.cpl")] static extern IntPtr BluetoothFindFirstRadio(ref FindParams p, out IntPtr radio);
  [DllImport("bthprops.cpl")] static extern bool BluetoothFindRadioClose(IntPtr h);
  [DllImport("bthprops.cpl")] static extern uint BluetoothSetServiceState(IntPtr radio, ref Info info, ref Guid service, uint flags);
  [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr h);
  public static uint Set(ulong addr, string svc, bool on) {
    var p = new FindParams(); p.Size = Marshal.SizeOf(typeof(FindParams));
    IntPtr radio; IntPtr f = BluetoothFindFirstRadio(ref p, out radio);
    if (f == IntPtr.Zero) return 0xFFFFFFFF;
    BluetoothFindRadioClose(f);
    var i = new Info(); i.Size = Marshal.SizeOf(typeof(Info)); i.Address = addr;
    var g = new Guid(svc);
    uint r = BluetoothSetServiceState(radio, ref i, ref g, on ? 1u : 0u);
    CloseHandle(radio);
    return r;
  }
}
'@
    $codes = @()
    foreach ($svc in '0000110b-0000-1000-8000-00805f9b34fb', '0000111e-0000-1000-8000-00805f9b34fb') {
      $codes += [TvhubBt]::Set($addr, $svc, $false)
    }
    Start-Sleep -Milliseconds 1200
    foreach ($svc in '0000110b-0000-1000-8000-00805f9b34fb', '0000111e-0000-1000-8000-00805f9b34fb') {
      $codes += [TvhubBt]::Set($addr, $svc, $true)
    }
    Emit @{ codes = $codes }
  }
}
`

const wifiScript = `
$act = $env:TVHUB_ACT
$a = Wifi
if (-not $a) { Emit @{ error = 'noadapter' }; exit }
switch ($act) {
  'scan' {
    AwaitAct ($a.ScanAsync()) 10000
    $cur = WifiCurrent $a
    $best = @{}
    foreach ($n in $a.NetworkReport.AvailableNetworks) {
      $s = [string]$n.Ssid
      if (-not $s) { continue }
      $bars = [int]$n.SignalBars
      if ($best.ContainsKey($s) -and $best[$s].bars -ge $bars) { continue }
      $auth = [string]$n.SecuritySettings.NetworkAuthenticationType
      $best[$s] = @{ ssid = $s; bars = $bars; secure = ($auth -ne 'Open80211' -and $auth -ne 'None'); connected = ($cur -and $cur.ssid -eq $s) }
    }
    Emit @{ networks = @($best.Values); current = $cur }
  }
  'connect' {
    AwaitAct ($a.ScanAsync()) 10000
    $net = $a.NetworkReport.AvailableNetworks | Where-Object { [string]$_.Ssid -eq $env:TVHUB_SSID } | Sort-Object -Property SignalBars -Descending | Select-Object -First 1
    if (-not $net) { Emit @{ error = 'notfound' }; break }
    $kind = [Windows.Devices.WiFi.WiFiReconnectionKind]::Automatic
    if ($env:TVHUB_PASS) {
      $cred = New-Object Windows.Security.Credentials.PasswordCredential
      $cred.Password = $env:TVHUB_PASS
      $res = Await ($a.ConnectAsync($net, $kind, $cred)) ([Windows.Devices.WiFi.WiFiConnectionResult]) 40000
    } else {
      $res = Await ($a.ConnectAsync($net, $kind)) ([Windows.Devices.WiFi.WiFiConnectionResult]) 40000
    }
    Emit @{ status = $(if ($res) { [string]$res.ConnectionStatus } else { 'Timeout' }) }
  }
  'disconnect' {
    $a.Disconnect()
    Emit @{ status = 'Success' }
  }
}
`

// runConnPS запускает сценарий PowerShell и разбирает его ответ (JSON в последней строке).
func runConnPS(script string, env map[string]string, timeout time.Duration, out any) error {
	ps := filepath.Join(os.Getenv("SystemRoot"), `System32\WindowsPowerShell\v1.0\powershell.exe`)
	full := strings.ReplaceAll(connPrelude, "{BT}", "`") + script
	u := utf16.Encode([]rune(full))
	b := make([]byte, len(u)*2)
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", base64.StdEncoding.EncodeToString(b))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	res, err := cmd.Output()
	lines := strings.Split(strings.TrimSpace(string(res)), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		if err != nil {
			return fmt.Errorf("Windows не ответила: %v", err)
		}
		return errors.New("Windows не ответила")
	}
	if e := json.Unmarshal([]byte(last), out); e != nil {
		log.Printf("bt/wifi: непонятный ответ: %s", last)
		return errors.New("непонятный ответ Windows")
	}
	return nil
}

type psDev struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Cls       string `json:"cls"`
	Paired    bool   `json:"paired"`
	Connected bool   `json:"connected"`
}

func toDevices(list []psDev) []btDevice {
	out := []btDevice{}
	for _, d := range list {
		out = append(out, btDevice{ID: d.ID, Name: d.Name, Kind: btKind(d.Cls, d.Name), Paired: d.Paired, Connected: d.Connected})
	}
	return out
}

var (
	connMu    sync.Mutex
	connCache map[string]any
	connAt    time.Time
)

func connStatus() map[string]any {
	connMu.Lock()
	defer connMu.Unlock()
	if connCache != nil && time.Since(connAt) < 8*time.Second {
		return connCache
	}
	var r struct {
		BT struct {
			Radio   string  `json:"radio"`
			Devices []psDev `json:"devices"`
		} `json:"bt"`
		Wifi struct {
			Adapter bool     `json:"adapter"`
			Current *wifiNet `json:"current"`
		} `json:"wifi"`
	}
	if err := runConnPS(connStatusScript, nil, 25*time.Second, &r); err != nil {
		log.Printf("bt/wifi: %v", err)
		return map[string]any{"error": err.Error()}
	}
	connCache = map[string]any{
		"bt":   map[string]any{"radio": strings.ToLower(r.BT.Radio), "devices": toDevices(r.BT.Devices)},
		"wifi": map[string]any{"adapter": r.Wifi.Adapter, "current": r.Wifi.Current},
	}
	connAt = time.Now()
	return connCache
}

func dropConnCache() {
	connMu.Lock()
	connCache = nil
	connMu.Unlock()
}

var btStatusText = map[string]string{
	"AccessDenied":                 "Windows не разрешила. Включите «Режим администратора» в «Настройки» → «Общие»",
	"AuthenticationFailure":        "Устройство не приняло подключение. Переведите его в режим сопряжения и попробуйте снова",
	"AuthenticationTimeout":        "Устройство не ответило вовремя. Переведите его в режим сопряжения и попробуйте снова",
	"ConnectionRejected":           "Устройство отклонило подключение",
	"NoSupportedProfiles":          "Windows не умеет работать с этим устройством",
	"PairingCanceled":              "Сопряжение отменено",
	"RejectedByHandler":            "Сопряжение отменено",
	"RemoteDeviceHasAssociation":   "Устройство уже подключено к другому компьютеру или телефону. Отключите его там",
	"Failed":                       "Не получилось. Переведите устройство в режим сопряжения и попробуйте снова",
	"OperationAlreadyInProgress":   "Windows уже подключает устройство, подождите",
	"RequiredHandlerNotRegistered": "Windows попросила подтверждение — посмотрите на экран компьютера",
}

func btAction(action, id string, on bool) (map[string]any, error) {
	defer dropConnCache()
	var r struct {
		Devices []psDev `json:"devices"`
		Status  string  `json:"status"`
		Error   string  `json:"error"`
		Codes   []int64 `json:"codes"`
		Events  int     `json:"events"` // сколько устройств нашёл поиск (для журнала)
		Total   int     `json:"total"`
	}
	env := map[string]string{"TVHUB_ACT": action, "TVHUB_ID": id}
	if on {
		env["TVHUB_ON"] = "1"
	}
	timeout := 40 * time.Second
	if action == "pair" {
		timeout = 75 * time.Second
	}
	if err := runConnPS(btScript, env, timeout, &r); err != nil {
		return nil, err
	}
	log.Printf("bluetooth %s: %s %s %v найдено=%d/%d с именем=%d", action, r.Status, r.Error, r.Codes, r.Events, r.Total, len(r.Devices))
	switch r.Error {
	case "noradio":
		return nil, errors.New("на компьютере нет Bluetooth")
	case "notfound":
		return nil, errors.New("устройство пропало. Нажмите «Искать» ещё раз")
	}
	switch action {
	case "scan":
		return map[string]any{"devices": toDevices(r.Devices)}, nil
	case "radio":
		if r.Status != "Allowed" {
			return nil, errors.New("Windows не дала переключить Bluetooth")
		}
	case "pair", "unpair":
		if r.Status != "Paired" && r.Status != "AlreadyPaired" && r.Status != "Unpaired" && r.Status != "AlreadyUnpaired" {
			if t, ok := btStatusText[r.Status]; ok {
				return nil, errors.New(t)
			}
			return nil, fmt.Errorf("не получилось (%s)", r.Status)
		}
	case "connect":
		for _, c := range r.Codes {
			if c == 5 {
				return nil, errors.New(btStatusText["AccessDenied"])
			}
		}
		// Windows подключается ещё пару секунд
		time.Sleep(3 * time.Second)
	}
	return nil, nil
}

var wifiStatusText = map[string]string{
	"InvalidCredential":                 "Неверный пароль",
	"AccessRevoked":                     "Windows не разрешила подключение. Включите «Расположение» в параметрах Windows (Конфиденциальность → Расположение) и разрешите его классическим приложениям",
	"NetworkNotAvailable":               "Сеть не найдена. Подойдите ближе к роутеру",
	"Timeout":                           "Сеть не ответила вовремя",
	"UnsupportedAuthenticationProtocol": "Windows не умеет подключаться к такой сети",
	"UnspecifiedFailure":                "Не получилось подключиться",
}

func wifiAction(action, ssid, pass string) (map[string]any, error) {
	defer dropConnCache()
	var r struct {
		Networks []wifiNet `json:"networks"`
		Current  *wifiNet  `json:"current"`
		Status   string    `json:"status"`
		Error    string    `json:"error"`
	}
	env := map[string]string{"TVHUB_ACT": action, "TVHUB_SSID": ssid, "TVHUB_PASS": pass}
	if err := runConnPS(wifiScript, env, 60*time.Second, &r); err != nil {
		return nil, err
	}
	log.Printf("wifi %s: %s %s", action, r.Status, r.Error)
	switch r.Error {
	case "noadapter":
		return nil, errors.New("на компьютере нет Wi-Fi")
	case "notfound":
		return nil, errors.New("сеть «" + ssid + "» не найдена")
	}
	switch action {
	case "scan":
		if r.Networks == nil {
			r.Networks = []wifiNet{}
		}
		if len(r.Networks) == 0 && r.Current == nil {
			return map[string]any{"networks": r.Networks, "hint": wifiStatusText["AccessRevoked"]}, nil
		}
		return map[string]any{"networks": r.Networks, "current": r.Current}, nil
	case "connect":
		if r.Status == "Success" {
			return nil, nil
		}
		// Без пароля к закрытой сети не подключиться — попросим его
		if pass == "" && (r.Status == "InvalidCredential" || r.Status == "UnspecifiedFailure") {
			return map[string]any{"needPassword": true}, nil
		}
		if t, ok := wifiStatusText[r.Status]; ok {
			return nil, errors.New(t)
		}
		return nil, fmt.Errorf("не получилось подключиться (%s)", r.Status)
	}
	return nil, nil
}
