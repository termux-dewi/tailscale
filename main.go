package tailscale

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
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
	authURL  string
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

		go func() {
			for {
				status, err := tsServer.Up(context.Background())
				if err == nil && status.BackendState == "Running" {
					isLogin = true
					authURL = ""
				} else if status != nil && status.AuthURL != "" {
					authURL = status.AuthURL
					isLogin = false
				}
				time.Sleep(2 * time.Second)
			}
		}()

		mux := http.NewServeMux()
		mux.HandleFunc("/", handleGuard)
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
	if authURL == "" {
		fmt.Fprintf(w, `<html><body style="background:#0f172a;color:white;text-align:center;padding-top:50px;font-family:sans-serif;"><h2>Initializing...</h2><script>setTimeout(function(){location.reload();}, 2000);</script></body></html>`)
		return
	}
	fmt.Fprintf(w, `<html><head><meta name="viewport" content="width=device-width, initial-scale=1.0"><style>body{background:#0f172a;color:white;font-family:sans-serif;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;}.card{background:#1e293b;padding:30px;border-radius:20px;width:85%%;text-align:center;}.btn-login{display:block;width:100%%;padding:15px;background:#3b82f6;color:white;text-decoration:none;border-radius:10px;font-weight:bold;margin-top:20px;}</style></head><body><div class="card"><h2>Tailscale Login</h2><a href="%s" target="_blank" class="btn-login">LOGIN KE TAILSCALE</a><script>setInterval(function(){ fetch('/').then(r => { if(r.status === 200 && !r.url.includes('login')) location.reload(); }); }, 4000);</script></div></body></html>`, authURL)
}

func renderDashboard(w http.ResponseWriter) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintf(w, `<html><head><meta name="viewport" content="width=device-width, initial-scale=1.0"><style>
		:root{--bg:#0f172a;--card:#1e293b;--primary:#3b82f6;--text:#f8fafc;}
		body{background:var(--bg);color:var(--text);font-family:sans-serif;margin:0;padding:10px;}
		.tabs{display:flex;margin-bottom:15px;background:var(--card);border-radius:10px;padding:5px;}
		.tab{flex:1;text-align:center;padding:10px;cursor:pointer;border-radius:8px;font-weight:bold;font-size:0.9em;}
		.tab.active{background:var(--primary);color:white;}
		.content{display:none;} .content.active{display:block;}
		.card{background:var(--card);padding:15px;border-radius:12px;margin-bottom:15px;}
		input,select,button{width:100%%;padding:12px;margin:8px 0;border-radius:8px;border:1px solid #334155;background:#0f172a;color:white;box-sizing:border-box;}
		.btn-submit{background:var(--primary);border:none;font-weight:bold;}
		table{width:100%%;font-size:0.8em;border-collapse:collapse;}
		td{padding:8px;border-top:1px solid #334155;}
		.btn-stop{background:#ef4444;border:none;padding:5px;border-radius:4px;color:white;}
	</style></head><body>
		<div class="tabs">
			<div class="tab active" onclick="openTab('client')">CLIENT MODE</div>
			<div class="tab" onclick="openTab('server')">SERVER MODE</div>
		</div>

		<div id="client" class="content active">
			<div class="card">
				<form action="/add" method="POST">
					<input type="hidden" name="mode" value="client">
					<input type="text" name="tip" placeholder="Target IP Tailscale" required>
					<input type="number" name="lp" placeholder="Local Port (Internal)" required>
					<input type="number" name="tp" placeholder="Target Port (Remote)" required>
					<select name="proto"><option value="tcp">TCP</option><option value="udp">UDP</option></select>
					<button type="submit" class="btn-submit">START CLIENT BRIDGE</button>
				</form>
			</div>
		</div>

		<div id="server" class="content">
			<div class="card">
				<form action="/add" method="POST">
					<input type="hidden" name="mode" value="server">
					<input type="number" name="lp" placeholder="Port di Tailscale (Public)" required>
					<input type="number" name="tp" placeholder="Ke Port HP (Internal)" required>
					<select name="proto"><option value="tcp">TCP</option><option value="udp">UDP</option></select>
					<button type="submit" class="btn-submit" style="background:#10b981;">START SERVER EXPOSE</button>
				</form>
			</div>
		</div>

		<div class="card">
			<h3>Active Bridges</h3>
			<table>`)
	for id, b := range bridges {
		fmt.Fprintf(w, "<tr><td>[%s] %s</td><td>%s:%s</td><td style='text-align:right;'><a href='/stop?id=%s'><button class='btn-stop'>OFF</button></a></td></tr>", b.Mode, b.Protocol, b.LocalPort, b.TargetPort, id)
	}
	fmt.Fprintf(w, `</table>
			<button onclick="window.location='/logout'" style="background:#475569;border:none;margin-top:15px;">LOGOUT</button>
		</div>

		<script>
			function openTab(name) {
				document.querySelectorAll('.content').forEach(c => c.classList.remove('active'));
				document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
				document.getElementById(name).classList.add('active');
				event.currentTarget.classList.add('active');
			}
		</script>
	</body></html>`)
}

func handleAdd(w http.ResponseWriter, r *http.Request) {
	lp, tp, tip, proto, mode := r.FormValue("lp"), r.FormValue("tp"), r.FormValue("tip"), r.FormValue("proto"), r.FormValue("mode")
	id := mode + proto + lp
	mu.Lock()
	if _, exists := bridges[id]; !exists {
		ctx, cancel := context.WithCancel(context.Background())
		b := &BridgeMap{id, lp, tp, tip, proto, mode, cancel}
		bridges[id] = b
		if mode == "client" {
			go startClientBridge(ctx, b)
		} else {
			go startServerBridge(ctx, b)
		}
	}
	mu.Unlock()
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func startClientBridge(ctx context.Context, b *BridgeMap) {
	ln, _ := net.Listen(b.Protocol, "127.0.0.1:"+b.LocalPort)
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

func startServerBridge(ctx context.Context, b *BridgeMap) {
	// Listen di jaringan Tailscale
	ln, _ := tsServer.Listen(b.Protocol, ":"+b.LocalPort)
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		c, err := ln.Accept(); if err != nil { return }
		go func(tsConn net.Conn) {
			defer tsConn.Close()
			// Forward ke port lokal HP
			localConn, err := net.Dial(b.Protocol, "127.0.0.1:"+b.TargetPort)
			if err != nil { return }
			defer localConn.Close()
			if b.Protocol == "tcp" { go io.Copy(tsConn, localConn); io.Copy(localConn, tsConn) } else { pipeUDP(tsConn, localConn) }
		}(c)
	}
}

func handleStop(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id"); mu.Lock()
	if b, ok := bridges[id]; ok { b.Cancel(); delete(bridges, id) }
	mu.Unlock(); http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	mu.Lock(); for _, b := range bridges { b.Cancel() }; bridges = make(map[string]*BridgeMap); mu.Unlock()
	if tsServer != nil { tsServer.Close() }; os.RemoveAll(dataDir); os.MkdirAll(dataDir, 0755)
	isLogin = false; authURL = ""; tsServer = &tsnet.Server{Hostname: "dual-mode-bridge", Dir: dataDir, Ephemeral: true}
	http.Redirect(w, r, "/", http.StatusSeeOther)
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
