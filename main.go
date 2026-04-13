package tailscale

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"tailscale.com/tsnet"
)

type BridgeMap struct {
	ID, LocalPort, TargetPort, TargetIP, Protocol, Mode string
	Cancel context.CancelFunc
}

var (
	tsServer *tsnet.Server
	bridges  = make(map[string]*BridgeMap)
	mu       sync.Mutex
	isLogin  = false
	dataDir  string
)

func StartEngine(storagePath string) {
	dataDir = storagePath
	go func() {
		tsServer = &tsnet.Server{
			Hostname: "dual-mode-bridge",
			Dir:      storagePath,
			Ephemeral: true,
		}

		// Check Auto-Login
		keyFile := filepath.Join(dataDir, "last_key.txt")
		if data, err := ioutil.ReadFile(keyFile); err == nil {
			tsServer.AuthKey = string(data)
			isLogin = true
		}

		mux := http.NewServeMux()
		mux.HandleFunc("/", handleGuard)
		mux.HandleFunc("/login", handleLogin)
		mux.HandleFunc("/logout", handleLogout)
		mux.HandleFunc("/add", handleAdd)
		mux.HandleFunc("/stop", handleStop)

		http.ListenAndServe("127.0.0.1:8080", mux)
	}()
}

func handleGuard(w http.ResponseWriter, r *http.Request) {
	if !isLogin {
		renderLoginPage(w)
		return
	}
	renderDashboard(w)
}

func renderLoginPage(w http.ResponseWriter) {
	fmt.Fprintf(w, `<html><head><meta name="viewport" content="width=device-width, initial-scale=1.0"><style>body{background:#0f172a;color:white;font-family:sans-serif;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;}.card{background:#1e293b;padding:25px;border-radius:15px;width:85%%;text-align:center;}input{width:100%%;padding:12px;margin:15px 0;border-radius:8px;background:#0f172a;color:white;border:1px solid #334155;}button{width:100%%;padding:12px;background:#3b82f6;color:white;border:none;border-radius:8px;font-weight:bold;}</style></head><body><div class="card"><h2>Tailscale Login</h2><form action="/login" method="POST"><input type="password" name="authkey" placeholder="tskey-auth-..." required><button type="submit">START ENGINE</button></form></div></body></html>`)
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { return }
	key := r.FormValue("authkey")
	
	// Simpan key untuk auto-login
	ioutil.WriteFile(filepath.Join(dataDir, "last_key.txt"), []byte(key), 0644)
	
	tsServer.AuthKey = key
	isLogin = true
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleLogout(w http.ResponseWriter) {
	mu.Lock()
	for _, b := range bridges { b.Cancel() }
	bridges = make(map[string]*BridgeMap)
	mu.Unlock()

	os.Remove(filepath.Join(dataDir, "last_key.txt"))
	tsServer.Close()
	isLogin = false
	// Re-init server for next login
	tsServer = &tsnet.Server{Hostname: "dual-mode-bridge", Dir: dataDir, Ephemeral: true}
	// Redirect will be handled by client
}

func renderDashboard(w http.ResponseWriter) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintf(w, `<html><head><meta name="viewport" content="width=device-width, initial-scale=1.0"><style>:root{--bg:#0f172a;--card:#1e293b;--primary:#3b82f6;--text:#f8fafc;}body{background:var(--bg);color:var(--text);font-family:sans-serif;padding:10px;}.card{background:var(--card);padding:15px;border-radius:12px;margin-bottom:15px;}input,select,button{width:100%%;padding:12px;margin:5px 0;border-radius:8px;border:1px solid #334155;background:#0f172a;color:white;}.btn-submit{background:var(--primary);border:none;font-weight:bold;}.btn-logout{background:#64748b;font-size:0.8em; margin-top:10px; border:none;}table{width:100%%;font-size:0.8em;border-collapse:collapse;}td{padding:8px;border-top:1px solid #334155;}.status-on{color:#22c55e;} .btn-stop{background:#ef4444; border:none; padding:5px; font-size:0.7em; border-radius:4px;}</style></head><body>
	<div class="card">
		<h3>🛰️ Multi Tunnel Bridge</h3>
		<form action="/add" method="POST">
			<input type="text" name="tip" placeholder="Target IP Tailscale">
			<input type="number" name="lp" placeholder="Local Port (Internal)">
			<input type="number" name="tp" placeholder="Target Port (Remote)">
			<select name="proto"><option value="tcp">TCP</option><option value="udp">UDP</option></select>
			<button type="submit" class="btn-submit">ADD & CONNECT</button>
		</form>
	</div>
	<div class="card">
		<h3>Active Tunnels</h3>
		<table>`)
	for id, b := range bridges {
		fmt.Fprintf(w, "<tr><td><b class='status-on'>●</b> %s</td><td>%s:%s</td><td><a href='/stop?id=%s'><button class='btn-stop'>OFF</button></a></td></tr>", b.Protocol, b.LocalPort, b.TargetPort, id)
	}
	fmt.Fprintf(w, `</table>
		<button onclick="window.location='/logout'" class="btn-logout">LOGOUT & CLEAR STATE</button>
	</div></body></html>`)
}

func handleAdd(w http.ResponseWriter, r *http.Request) {
	lp, tp, tip, proto := r.FormValue("lp"), r.FormValue("tp"), r.FormValue("tip"), r.FormValue("proto")
	id := proto + lp
	mu.Lock()
	if _, exists := bridges[id]; !exists {
		ctx, cancel := context.WithCancel(context.Background())
		b := &BridgeMap{id, lp, tp, tip, proto, "Client", cancel}
		bridges[id] = b
		go startBridge(ctx, b)
	}
	mu.Unlock()
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleStop(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	mu.Lock()
	if b, ok := bridges[id]; ok {
		b.Cancel()
		delete(bridges, id)
	}
	mu.Unlock()
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func startBridge(ctx context.Context, b *BridgeMap) {
	ln, err := net.Listen(b.Protocol, "127.0.0.1:"+b.LocalPort)
	if err != nil { return }
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		c, err := ln.Accept(); if err != nil { return }
		go func(in net.Conn) {
			defer in.Close()
			out, err := tsServer.Dial(ctx, b.Protocol, net.JoinHostPort(b.TargetIP, b.TargetPort))
			if err != nil { return }
			defer out.Close()
			if b.Protocol == "tcp" { go io.Copy(in, out); io.Copy(out, in) } else { pipeUDP(in, out) }
		}(c)
	}
}

func pipeUDP(c1, c2 net.Conn) {
	go func() {
		buf := make([]byte, 2048)
		for {
			c1.SetReadDeadline(time.Now().Add(30*time.Second))
			n, err := c1.Read(buf); if err != nil { return }
			c2.Write(buf[:n])
		}
	}()
	buf := make([]byte, 2048)
	for {
		c2.SetReadDeadline(time.Now().Add(30*time.Second))
		n, err := c2.Read(buf); if err != nil { return }
		c1.Write(buf[:n])
	}
}
