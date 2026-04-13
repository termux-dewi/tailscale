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
}

var (
	tsServer *tsnet.Server
	bridges  = make(map[string]*BridgeMap)
	mu       sync.Mutex
	isLogin  = false
)

// StartEngine diekspor ke Java. Menerima path internal storage.
func StartEngine(storagePath string) {
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/", handleGuard)
		mux.HandleFunc("/login", handleLogin)
		mux.HandleFunc("/add", handleAdd)
		mux.HandleFunc("/stop", handleStop)

		// Konfigurasi Server Tailscale
		tsServer = &tsnet.Server{
			Hostname: "dual-mode-bridge",
			Dir:      storagePath, 
			Ephemeral: true,
		}

		log.Println("Engine Go berjalan di 127.0.0.1:8080")
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
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	tsServer.AuthKey = key
	if _, err := tsServer.Up(ctx); err != nil {
		fmt.Fprintf(w, "<script>alert('Error: %v'); window.location='/';</script>", err)
		return
	}
	isLogin = true
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func renderDashboard(w http.ResponseWriter) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintf(w, `<html><head><meta name="viewport" content="width=device-width, initial-scale=1.0"><style>:root{--bg:#0f172a;--card:#1e293b;--primary:#3b82f6;--text:#f8fafc;}body{background:var(--bg);color:var(--text);font-family:sans-serif;padding:10px;}.card{background:var(--card);padding:15px;border-radius:12px;margin-bottom:15px;}input,select,button{width:100%%;padding:12px;margin:5px 0;border-radius:8px;border:1px solid #334155;background:#0f172a;color:white;}.btn-submit{background:var(--primary);border:none;font-weight:bold;}table{width:100%%;font-size:0.8em;border-collapse:collapse;}td{padding:8px;border-top:1px solid #334155;}</style></head><body><div class="card"><h3>🛰️ Create Tunnel</h3><form action="/add" method="POST"><input type="text" name="tip" placeholder="Target IP Tailscale"><input type="number" name="lp" placeholder="Local Port"><input type="number" name="tp" placeholder="Target Port"><select name="proto"><option value="tcp">TCP</option><option value="udp">UDP</option></select><button type="submit" class="btn-submit">CONNECT</button></form></div><div class="card"><table>`)
	for _, b := range bridges {
		fmt.Fprintf(w, "<tr><td><b>%s</b></td><td>%s -> %s</td></tr>", b.Mode, b.LocalPort, b.TargetPort)
	}
	fmt.Fprintf(w, `</table></div></body></html>`)
}

func handleAdd(w http.ResponseWriter, r *http.Request) {
	lp, tp, tip, proto := r.FormValue("lp"), r.FormValue("tp"), r.FormValue("tip"), r.FormValue("proto")
	id := proto + lp
	mu.Lock()
	if _, exists := bridges[id]; !exists {
		ctx, cancel := context.WithCancel(context.Background())
		bridges[id] = &BridgeMap{id, lp, tp, tip, proto, "Client"}
		go func() {
			ln, _ := net.Listen(proto, "127.0.0.1:"+lp)
			go func() { <-ctx.Done(); ln.Close() }()
			for {
				c, err := ln.Accept(); if err != nil { return }
				go func(in net.Conn) {
					defer in.Close()
					out, err := tsServer.Dial(ctx, proto, net.JoinHostPort(tip, tp))
					if err != nil { return }
					defer out.Close()
					if proto == "tcp" { go io.Copy(in, out); io.Copy(out, in) } else { pipeUDP(in, out) }
				}(c)
			}
		}()
		_ = cancel // Simpan jika butuh stop logic
	}
	mu.Unlock()
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleStop(w http.ResponseWriter, r *http.Request) {
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
