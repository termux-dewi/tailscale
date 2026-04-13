package tailscale

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"tailscale.com/tsnet"
)

type BridgeMap struct {
	ID, LocalPort, TargetPort, TargetIP, Protocol, Mode string
	cancel context.CancelFunc
}

var (
	tsServer *tsnet.Server
	bridges  = make(map[string]*BridgeMap)
	mu       sync.Mutex
	isLogin  = false
)

func main() {
	http.HandleFunc("/", handleGuard)
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/add", handleAdd)
	http.HandleFunc("/stop", handleStop)

	log.Println("Dual Engine aktif di port 8080...")
	log.Fatal(http.ListenAndServe("127.0.0.1:8080", nil))
}

// --- AUTH GUARD ---
func handleGuard(w http.ResponseWriter, r *http.Request) {
	if !isLogin {
		renderLoginPage(w)
		return
	}
	renderDashboard(w)
}

func renderLoginPage(w http.ResponseWriter) {
	fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head>
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <style>
        body { background: #0f172a; color: white; font-family: sans-serif; display: flex; justify-content: center; align-items: center; height: 100vh; margin: 0; }
        .login-card { background: #1e293b; padding: 25px; border-radius: 15px; width: 85%%; max-width: 350px; text-align: center; }
        input { width: 100%%; padding: 12px; margin: 15px 0; border-radius: 8px; border: 1px solid #334155; background: #0f172a; color: white; box-sizing: border-box; }
        button { width: 100%%; padding: 12px; background: #3b82f6; border: none; color: white; font-weight: bold; border-radius: 8px; }
    </style>
</head>
<body>
    <div class="login-card">
        <h2>Tailscale Login</h2>
        <form action="/login" method="POST">
            <input type="password" name="authkey" placeholder="tskey-auth-..." required>
            <button type="submit">START ENGINE</button>
        </form>
    </div>
</body>
</html>`)
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { return }
	key := r.FormValue("authkey")
	tsServer = &tsnet.Server{Hostname: "dual-mode-bridge", Dir: "./ts-state", AuthKey: key, Ephemeral: true}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := tsServer.Up(ctx); err != nil {
		fmt.Fprintf(w, "<script>alert('Error: %v'); window.location='/';</script>", err)
		return
	}
	isLogin = true
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- DASHBOARD WITH SWITCH MODE ---
func renderDashboard(w http.ResponseWriter) {
	mu.Lock()
	defer mu.Unlock()

	fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head>
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <style>
        :root { --bg: #0f172a; --card: #1e293b; --primary: #3b82f6; --secondary: #8b5cf6; --text: #f8fafc; }
        body { background: var(--bg); color: var(--text); font-family: sans-serif; margin: 0; padding: 10px; }
        
        /* Tab System */
        .tabs { display: flex; background: var(--card); border-radius: 10px; padding: 5px; margin-bottom: 15px; }
        .tab-btn { flex: 1; padding: 10px; border: none; background: none; color: #94a3b8; font-weight: bold; cursor: pointer; border-radius: 8px; }
        .tab-btn.active { background: var(--primary); color: white; }
        .tab-content { display: none; }
        .tab-content.active { display: block; }

        .card { background: var(--card); padding: 15px; border-radius: 12px; margin-bottom: 15px; box-shadow: 0 4px 6px rgba(0,0,0,0.2); }
        input, select, button { width: 100%%; padding: 12px; margin: 5px 0; border-radius: 8px; border: 1px solid #334155; background: #0f172a; color: white; box-sizing: border-box; }
        .btn-submit { background: var(--primary); border: none; font-weight: bold; }
        .host-theme .btn-submit { background: var(--secondary); }
        .btn-stop { background: #ef4444; border: none; padding: 5px 10px; font-size: 0.7em; color: white; border-radius: 5px; }
        table { width: 100%%; font-size: 0.85em; margin-top: 10px; border-collapse: collapse; }
        td { padding: 8px 0; border-top: 1px solid #334155; }
    </style>
</head>
<body>
    <div class="tabs">
        <button class="tab-btn active" onclick="switchMode('client', this)">Client Mode</button>
        <button class="tab-btn" onclick="switchMode('host', this)">Host Mode</button>
    </div>

    <div id="client" class="tab-content active">
        <div class="card">
            <h3 style="color:var(--primary); margin:0 0 10px 0;">🛰️ Client Mode</h3>
            <form action="/add" method="POST">
                <input type="hidden" name="mode" value="Client">
                <input type="text" name="tip" placeholder="Target IP Tailscale" required>
                <input type="number" name="lp" placeholder="Local Port (Internal)" required>
                <input type="number" name="tp" placeholder="Remote Port (Target)" required>
                <select name="proto"><option value="tcp">TCP</option><option value="udp">UDP</option></select>
                <button type="submit" class="btn-submit">CONNECT BRIDGE</button>
            </form>
        </div>
    </div>

    <div id="host" class="tab-content host-theme">
        <div class="card">
            <h3 style="color:var(--secondary); margin:0 0 10px 0;">🏠 Host Mode</h3>
            <form action="/add" method="POST">
                <input type="hidden" name="mode" value="Host">
                <input type="number" name="tp" placeholder="Port di Tailscale" required>
                <input type="number" name="lp" placeholder="Port Lokal HP" required>
                <select name="proto"><option value="tcp">TCP</option><option value="udp">UDP</option></select>
                <button type="submit" class="btn-submit">ACTIVATE HOST</button>
            </form>
        </div>
    </div>

    <div class="card">
        <h3 style="font-size:0.9em; color:#94a3b8; margin:0 0 10px 0;">Active Tunnels</h3>
        <table>`)
	for id, b := range bridges {
		color := "var(--primary)"
		if b.Mode == "Host" { color = "var(--secondary)" }
		fmt.Fprintf(w, "<tr><td style='color:%s'><b>%s</b></td><td>%s:%s</td><td><a href='/stop?id=%s'><button class='btn-stop'>STOP</button></a></td></tr>", color, b.Mode, b.LocalPort, b.TargetPort, id)
	}
	fmt.Fprintf(w, `
        </table>
    </div>

    <script>
        function switchMode(mode, btn) {
            document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
            document.querySelectorAll('.tab-btn').forEach(b => b.classList.remove('active'));
            document.getElementById(mode).classList.add('active');
            btn.classList.add('active');
        }
    </script>
</body>
</html>`)
}

// --- CORE LOGIC (Add, Stop, Bridge, Pipe) ---
// Sama seperti kode sebelumnya, tambahkan di sini untuk melengkapi biner.

func handleAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { return }
	mode, lp, tp, tip, proto := r.FormValue("mode"), r.FormValue("lp"), r.FormValue("tp"), r.FormValue("tip"), r.FormValue("proto")
	id := mode + proto + lp
	mu.Lock()
	if _, exists := bridges[id]; !exists {
		ctx, cancel := context.WithCancel(context.Background())
		bridges[id] = &BridgeMap{id, lp, tp, tip, proto, mode, cancel}
		if mode == "Client" { go startClient(ctx, bridges[id]) } else { go startHost(ctx, bridges[id]) }
	}
	mu.Unlock()
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleStop(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	mu.Lock()
	if b, exists := bridges[id]; exists { b.cancel(); delete(bridges, id) }
	mu.Unlock()
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func startClient(ctx context.Context, b *BridgeMap) {
	ln, _ := net.Listen(b.Protocol, "127.0.0.1:"+b.LocalPort)
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		c, err := ln.Accept(); if err != nil { return }
		go func(in net.Conn) {
			defer in.Close()
			out, err := tsServer.Dial(ctx, b.Protocol, net.JoinHostPort(b.TargetIP, b.TargetPort))
			if err != nil { return }
			defer out.Close()
			pipe(in, out, b.Protocol)
		}(c)
	}
}

func startHost(ctx context.Context, b *BridgeMap) {
	ln, _ := tsServer.Listen(b.Protocol, ":"+b.TargetPort)
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		c, err := ln.Accept(); if err != nil { return }
		go func(in net.Conn) {
			defer in.Close()
			out, err := net.Dial(b.Protocol, "127.0.0.1:"+b.LocalPort)
			if err != nil { return }
			defer out.Close()
			pipe(in, out, b.Protocol)
		}(c)
	}
}

func pipe(c1, c2 net.Conn, proto string) {
	if proto == "tcp" { go io.Copy(c1, c2); io.Copy(c2, c1) } else {
		go func() {
			buf := make([]byte, 2048)
			for { c1.SetReadDeadline(time.Now().Add(30*time.Second)); n, _ := c1.Read(buf); if n==0 {return}; c2.Write(buf[:n]) }
		}()
		buf := make([]byte, 2048)
		for { c2.SetReadDeadline(time.Now().Add(30*time.Second)); n, _ := c2.Read(buf); if n==0 {return}; c1.Write(buf[:n]) }
	}
}
