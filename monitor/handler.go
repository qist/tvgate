package monitor

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/qist/tvgate/config"
)

// 缓存解析后的 HTML 模板，避免每次请求重新解析
var (
	statusTemplate     *template.Template
	statusTemplateOnce sync.Once
)

// 页面数据结构
type StatusData struct {
	Timestamp     time.Time
	Uptime        time.Duration
	Version       string
	Goroutines    int
	MemoryStats   runtime.MemStats
	ProxyGroups   map[string]*config.ProxyGroupConfig
	TrafficStats  *TrafficStats
	ClientIP      string
	ActiveClients []*ClientConnection
	WebPath       string
}

// HTTP 处理入口
func HandleMonitor(w http.ResponseWriter, r *http.Request) {
	// w.Header().Set("server", "TVGate")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Header.Get("Accept") == "application/json" || r.URL.Query().Get("format") == "json" {
		handleJSONRequest(w, r)
		return
	}
	handleHTMLRequest(w, r)
}

func handleJSONRequest(w http.ResponseWriter, r *http.Request) {
	data := prepareStatusData(r)
	// w.Header().Set("server", "TVGate")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func handleHTMLRequest(w http.ResponseWriter, r *http.Request) {
	data := prepareStatusData(r)

	tmpl := `<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8">
<title> 状态监控</title>
<style>
body { 
    font-family: 'Segoe UI', sans-serif; 
    max-width:1200px;
    margin:20px auto;
    background:#121212; 
    color:#e0e0e0;
}
.header {
    background:#1f1f1f; 
    color:white; 
    padding:20px; 
    border-radius:10px;
    margin-bottom:20px;
    box-shadow:0 2px 8px rgba(0,0,0,0.5);
}
.header h1 {margin:0;}
.table {
    width:100%; 
    border-collapse: collapse; 
    margin-bottom:20px; 
    table-layout:fixed; 
    word-wrap:break-word;
}
.table th, .table td {
    border:1px solid #333; 
    padding:8px; 
    text-align:left; 
    max-width:200px; 
    white-space:nowrap; 
    overflow:hidden; 
    text-overflow:ellipsis;
}
.table th {
    background:#1f1f1f;
}
.table tr:nth-child(even) {background:#181818;}
.table tr:hover {background:#2a2a2a;}
.table td.url-cell {max-width:700px;}
.table td.ua-cell {max-width:200px;}
.status-alive {color:#4CAF50;font-weight:bold;}
.status-dead {color:#f44336;font-weight:bold;}
.status-cooldown {color:#ff9800;font-weight:bold;}
.status-unknown {color:#9E9E9E;font-weight:bold;}
.refresh-controls {margin:10px 0 20px; display:flex; align-items:center; gap:10px;}
.refresh-btn {border:none; padding:8px 15px; border-radius:5px; font-weight:bold; cursor:pointer;}
.refresh-btn {border:none; padding:8px 15px; border-radius:5px; font-weight:bold; cursor:pointer;}
.refresh-on {background:#4CAF50; color:white;}
.refresh-off {background:#f44336; color:white;}
.toggle-column {cursor:pointer; user-select:none;}
.theme-btn {
    border:none; 
    padding:8px 15px; 
    border-radius:5px; 
    font-weight:bold; 
    cursor:pointer;
    background:#555;
    color:white;
    margin-left:10px;
}
.card {
    background: #1f1f1f;
    padding: 15px; 
    border-radius: 8px; 
    box-shadow: 0 1px 3px rgba(0,0,0,0.5);
}
.card h3 { margin-top:0; color:#e0e0e0; }
.card ul li { padding:5px 0; }
</style>
</head>
<body>

<div class="header">
<h1> 状态监控</h1>
<p>更新时间: {{.Timestamp.Format "2006-01-02 15:04:05"}}</p>
</div>

<div class="refresh-controls">
<button id="toggleRefresh" class="refresh-btn">⟳ 自动刷新</button>
<label for="interval">间隔:</label>
<select id="interval">
<option value="1000">1s</option>
<option value="3000">3s</option>
<option value="5000">5s</option>
<option value="10000">10s</option>
<option value="30000">30s</option>
</select>
<button id="toggleTheme" class="theme-btn">🌓 切换主题</button>
</div>

<h2>系统信息</h2>
<div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(250px, 1fr)); gap: 15px; margin-bottom: 20px;">
  <div class="card">
    <h3>基础信息</h3>
    <ul style="list-style: none; padding: 0;">
      <li><strong>操作系统:</strong> {{.TrafficStats.HostInfo.Platform}}</li>
      <li><strong>内核版本:</strong> {{.TrafficStats.HostInfo.KernelVersion}}</li>
      <li><strong>CPU架构:</strong> {{.TrafficStats.HostInfo.KernelArch}}</li>
      <li><strong>版本:</strong> {{.Version}}</li>
      <li><strong>运行时间:</strong> 
        {{$totalSeconds := .Uptime.Seconds}}
        {{$days := float64ToInt64 (divFloat64 $totalSeconds 86400)}}
        {{$hours := float64ToInt64 (divFloat64 (modFloat64 $totalSeconds 86400) 3600)}}
        {{$minutes := float64ToInt64 (divFloat64 (modFloat64 $totalSeconds 3600) 60)}}
        {{$seconds := float64ToInt64 (modFloat64 $totalSeconds 60)}}
        {{if gt $days 0}}{{$days}}天{{end}}{{if gt $hours 0}}{{$hours}}小时{{end}}{{if gt $minutes 0}}{{$minutes}}分{{end}}{{$seconds}}秒
      </li>
      <li><strong>Goroutines:</strong> {{.Goroutines}}</li>
      <li><strong>客户端IP:</strong> {{.ClientIP}}</li>
    </ul>
  </div>
  
  <div class="card">
    <h3>网络流量</h3>
    <ul style="list-style: none; padding: 0;">
      <li><strong>总流量:</strong> {{FormatBytes .TrafficStats.TotalBytes}}</li>
      <li><strong>入口流量:</strong> {{FormatBytes .TrafficStats.InboundBytes}}</li>
      <li><strong>出口流量:</strong> {{FormatBytes .TrafficStats.OutboundBytes}}</li>
      <li><strong>实时总带宽(入):</strong> {{FormatNetworkBandwidth .TrafficStats.InboundBandwidth}}</li>
      <li><strong>实时总带宽(出):</strong> {{FormatNetworkBandwidth .TrafficStats.OutboundBandwidth}}</li>
    </ul>
  </div>
  
  <div class="card">
    <h3>CPU与内存</h3>
    <ul style="list-style: none; padding: 0;">
      <li><strong>系统负载:</strong> {{printf "%.2f" .TrafficStats.LoadAverage.Load1}} / {{printf "%.2f" .TrafficStats.LoadAverage.Load5}} / {{printf "%.2f" .TrafficStats.LoadAverage.Load15}}</li>
      <li><strong>CPU核心数:</strong> {{.TrafficStats.CPUCount}}</li> 
	  <li><strong>CPU 使用率:</strong> {{printf "%.2f%%" .TrafficStats.CPUUsage}}</li>
	  {{if ge .TrafficStats.CPUTemperature 0.0}}<li><strong>CPU 温度:</strong> {{printf "%.2f°C" .TrafficStats.CPUTemperature}}</li>{{end}}
	  <li><strong>总内存:</strong> {{FormatBytes .TrafficStats.MemoryTotal}}</li>
      <li><strong>内存使用:</strong> {{FormatBytes .TrafficStats.MemoryUsage}}</li>
    </ul>
  </div>
  
  <div class="card">
    <h3>监控</h3>
    <ul style="list-style: none; padding: 0;">
      <li><strong>CPU:</strong> {{printf "%.2f%%" .TrafficStats.App.CPUPercent}} <small style="color:#aaa; font-size:10px;">（多核 CPU 时可能超过 100%）</small></li>
      <li><strong>内存:</strong> {{FormatBytes .TrafficStats.App.MemoryUsage}}</li>
    </ul>
  </div>
</div>

<div style="display: grid; grid-template-columns: 1fr 1fr; gap: 15px; margin-bottom: 20px;">
  <div class="card">
    <h3>存储信息</h3>
    {{if .TrafficStats.DiskPartitions}}
    <table class="table">
      <thead>
        <tr>
          <th>挂载点</th>
          <th>文件系统</th>
          <th>已用/总量</th>
          <th>使用率</th>
        </tr>
      </thead>
      <tbody>
        {{range .TrafficStats.DiskPartitions}}
        <tr>
          <td>{{.MountPoint}}</td>
          <td>{{.FsType}}</td>
          <td>{{FormatBytes .Used}} / {{FormatBytes .Total}}</td>
          <td>
            <div style="display: flex; align-items: center;">
              <div style="width: 100%; background-color: #333; border-radius: 4px; height: 16px; margin-right: 8px;">
                <div style="width: {{if gt .UsedPercent 100.0}}100{{else if lt .UsedPercent 0.0}}0{{else}}{{printf "%.0f" .UsedPercent}}{{end}}%; height: 16px; background-color: {{if gt .UsedPercent 90.0}}#dc3545{{else if gt .UsedPercent 75.0}}#ffc107{{else}}#28a745{{end}}; border-radius: 4px;"></div>
              </div>
              <span>{{printf "%.2f%%" .UsedPercent}}</span>
            </div>
          </td>
        </tr>
        {{end}}
      </tbody>
    </table>
    {{end}}
  </div>
  
  <div class="card">
    <h3>各网卡流量详情</h3>
    {{if .TrafficStats.NetworkInterfaces}}
    <table class="table" style="font-size:0.9em;">
      <thead>
        <tr>
          <th>网卡</th>
          <th>接收</th>
          <th>发送</th>
          <th>接收带宽</th>
          <th>发送带宽</th>
        </tr>
      </thead>
      <tbody>
        {{range .TrafficStats.NetworkInterfaces}}
        <tr>
          <td>{{.Name}}</td>
          <td>{{FormatBytes .BytesRecv}}</td>
          <td>{{FormatBytes .BytesSent}}</td>
          <td>{{FormatNetworkBandwidth .RecvBandwidth}}</td>
          <td>{{FormatNetworkBandwidth .SendBandwidth}}</td>
        </tr>
        {{end}}
      </tbody>
    </table>
    {{end}}
  </div>
</div>

<h2>活跃客户端连接</h2>
<table class="table">
<tr>
<th style="width: 300px;">IP</th>
<th style="width: 400px;">URL</th>
<th style="width: 80px;">类型</th>
<th style="width: 150px;">UA</th>
<th style="text-align:center; width: 80px;">连接时间</th>
<th style="text-align:center; width: 80px;">最后活跃</th>
</tr>
{{range .ActiveClients}}
<tr>
<td style="word-break: break-all;">{{.IP}}</td>
<td class="url-cell" style="word-break: break-all;" title="{{.URL}}">{{.URL}}</td>
<td>{{.ConnectionType}}</td>
<td class="ua-cell" style="word-break: break-word;" title="{{.UserAgent}}">{{.UserAgent}}</td>
<td style="text-align:center;">{{.ConnectedAt.Format "15:04:05"}}</td>
<td style="text-align:center;">{{.LastActive.Format "15:04:05"}}</td>
</tr>
{{end}}
</table>

<h2>代理组状态</h2>
{{range $name, $group := .ProxyGroups}}
<h3>{{$name}} (负载均衡: {{$group.LoadBalance}})</h3>
<table class="table">
<tr>
<th>代理</th>
<th>延迟 <span class="toggle-column" data-column="1" data-group="{{$name}}">👁</span></th>
<th>类型 <span class="toggle-column" data-column="2" data-group="{{$name}}">👁</span></th>
<th>服务器 <span class="toggle-column" data-column="3" data-group="{{$name}}">👁</span></th>
<th>HTTP状态</th>
<th>状态</th>
</tr>
{{range $proxy := $group.Proxies}}
<tr>
<td>{{$proxy.Name}}</td>
<td data-column="1" data-group="{{$name}}" data-value="{{ $stats := index $group.Stats.ProxyStats $proxy.Name }}{{if $stats}}{{if gt $stats.ResponseTime 0}}{{printf "%.0f ms" (divInt64 $stats.ResponseTime.Nanoseconds 1000000)}}{{end}}{{end}}">*</td>
<td data-column="2" data-group="{{$name}}" data-value="{{if $proxy.Type}}{{$proxy.Type}}{{end}}">*</td>
<td data-column="3" data-group="{{$name}}" data-value="{{if $proxy.Server}}{{$proxy.Server}}{{end}}">*</td>
  <td>
    {{ $stats := index $group.Stats.ProxyStats $proxy.Name }}
    {{if $stats}}{{if gt $stats.StatusCode 0}}{{$stats.StatusCode}}{{else}}-{{end}}{{else}}-{{end}}
  </td>
<td>
{{ $stats := index $group.Stats.ProxyStats $proxy.Name }}
{{if $stats}}
{{if and $stats.Alive (or (gt $stats.ResponseTime 0) (gt $stats.FailCount 0))}}<span class="status-alive">✅ 活跃</span>
{{else if $stats.CooldownUntil.After $.Timestamp}}<span class="status-cooldown">🚫 冷却</span>
{{else if and (not $stats.Alive) (or (gt $stats.ResponseTime 0) (gt $stats.FailCount 0))}}<span class="status-dead">❌ 失败</span>
{{else}}<span class="status-unknown">⚪ 未测试</span>
{{end}}
{{else}}<span class="status-unknown">⚪ 未初始化</span>{{end}}
</td>
</tr>
{{end}}
</table>
{{end}}

<script>
let refreshMs = parseInt(localStorage.getItem('refreshMs')) || 3000;
let auto = localStorage.getItem('autoRefresh') !== 'false';
let timer = null;
const toggleBtn = document.getElementById('toggleRefresh');
const intervalSelect = document.getElementById('interval');
if(intervalSelect.querySelector('option[value="'+refreshMs+'"]')) intervalSelect.value = refreshMs;

function applyButtonUI(){
    if(auto){
        toggleBtn.textContent = '⟳ 自动刷新 (' + (refreshMs/1000) + 's)';
        toggleBtn.className = 'refresh-btn refresh-on';
    }else{
        toggleBtn.textContent = '⏸ 刷新已暂停';
        toggleBtn.className = 'refresh-btn refresh-off';
    }
}

function stopTimer(){ if(timer){ clearInterval(timer); timer=null; } }
function startTimer(){ stopTimer(); timer=setInterval(()=>{location.reload();}, refreshMs); }
function persist(){ localStorage.setItem('autoRefresh', auto); localStorage.setItem('refreshMs', refreshMs); }

if(auto) startTimer();
applyButtonUI();

document.querySelectorAll('.toggle-column').forEach(el => {
    el.addEventListener('click', function() {
        const colIndex = this.getAttribute('data-column');
        const group = this.getAttribute('data-group');
        const tds = document.querySelectorAll('td[data-column="'+colIndex+'"][data-group="'+group+'"]');
        tds.forEach(td => {
            const realValue = td.getAttribute('data-value');
            if(td.textContent === "*"){ td.textContent = realValue || ""; } 
            else { td.textContent = "*"; }
        });
    });
});

toggleBtn.onclick=()=>{ auto=!auto; if(auto) startTimer(); else stopTimer(); applyButtonUI(); persist(); };
intervalSelect.onchange=()=>{ refreshMs=parseInt(intervalSelect.value); if(auto) startTimer(); applyButtonUI(); persist(); };

// 主题切换功能
const toggleThemeBtn = document.getElementById('toggleTheme');
let currentTheme = localStorage.getItem('theme') || 'dark';
const webBase = '{{.WebPath}}';
// 同步主题到后端
async function syncThemeToBackend(theme) {
    try {
        const response = await fetch(webBase + 'sync-theme', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({ theme: theme })
        });
        const result = await response.json();
        if (result.status !== 'success') {
            console.error('主题同步失败:', result);
        }
    } catch (error) {
        console.error('主题同步请求失败:', error);
    }
}

// 从后端获取当前主题
async function getCurrentThemeFromBackend() {
    try {
        const response = await fetch(webBase + 'sync-theme');
        const result = await response.json();
        if (result.theme && (result.theme === 'dark' || result.theme === 'light')) {
            return result.theme;
        }
    } catch (error) {
        console.error('获取主题失败:', error);
    }
    return null;
}

function applyTheme() {
    if (currentTheme === 'light') {
        document.body.style.backgroundColor = '#ffffff';
        document.body.style.color = '#333333';
        document.querySelectorAll('.card, .header').forEach(el => {
            el.style.backgroundColor = '#f5f5f5';
            el.style.color = '#333333';
        });
        document.querySelectorAll('.table th').forEach(el => {
            el.style.backgroundColor = '#f5f5f5';
        });
        document.querySelectorAll('.table tr:nth-child(even)').forEach(el => {
            el.style.backgroundColor = '#f0f0f0';
        });
    } else {
        document.body.style.backgroundColor = '#121212';
        document.body.style.color = '#e0e0e0';
        document.querySelectorAll('.card, .header').forEach(el => {
            el.style.backgroundColor = '#1f1f1f';
            el.style.color = '#e0e0e0';
        });
        document.querySelectorAll('.table th').forEach(el => {
            el.style.backgroundColor = '#1f1f1f';
        });
        document.querySelectorAll('.table tr:nth-child(even)').forEach(el => {
            el.style.backgroundColor = '#181818';
        });
    }
    localStorage.setItem('theme', currentTheme);
    syncThemeToBackend(currentTheme);
}

toggleThemeBtn.addEventListener('click', () => {
    currentTheme = currentTheme === 'dark' ? 'light' : 'dark';
    applyTheme();
});

// 初始化应用主题
(async function initTheme() {
    // 优先使用本地存储的主题
    const localTheme = localStorage.getItem('theme');
    if (localTheme === 'dark' || localTheme === 'light') {
        currentTheme = localTheme;
    }
    
    // 从后端获取主题并比较
    const backendTheme = await getCurrentThemeFromBackend();
    if (backendTheme) {
        // 如果本地没有主题或与后端不一致，使用后端主题
        if (!localTheme || backendTheme !== currentTheme) {
            currentTheme = backendTheme;
            localStorage.setItem('theme', currentTheme);
        }
    }
    
    applyTheme();
    
    // 监听页面可见性变化，当页面重新获得焦点时同步主题
    document.addEventListener('visibilitychange', async () => {
        if (!document.hidden) {
            const backendTheme = await getCurrentThemeFromBackend();
            if (backendTheme && backendTheme !== currentTheme) {
                currentTheme = backendTheme;
                localStorage.setItem('theme', currentTheme);
                applyTheme();
            }
        }
    });
    
    // 添加主题变化事件监听器
    window.addEventListener('storage', (event) => {
        if (event.key === 'theme' && (event.newValue === 'dark' || event.newValue === 'light')) {
            currentTheme = event.newValue;
            applyTheme();
        }
    });
})();
</script>

</body>
</html>`

	// 使用 sync.Once 缓存解析后的模板，避免每次请求重新解析
	statusTemplateOnce.Do(func() {
		statusTemplate, _ = template.New("status").Funcs(template.FuncMap{
			"divInt64": func(a int64, b ...int64) float64 {
				result := float64(a)
				for _, v := range b {
					if v != 0 {
						result /= float64(v)
					}
				}
				return result
			},
			"modInt64": func(a int64, b int64) int64 {
				if b != 0 {
					return a % b
				}
				return 0
			},
			"divFloat64": func(a float64, b ...float64) float64 {
				result := a
				for _, v := range b {
					if v != 0 {
						result /= v
					}
				}
				return result
			},
			"modFloat64": func(a, b float64) float64 {
				if b != 0 {
					return float64(int64(a) % int64(b))
				}
				return 0
			},
			"float64ToInt64":         func(a float64) int64 { return int64(a) },
			"FormatBytes":            FormatBytes,
			"FormatBytesPerSec":      FormatBytesPerSec,
			"FormatNetworkBandwidth": FormatNetworkBandwidth,
			"ge": func(a, b float64) bool { return a >= b }, // 添加ge函数用于温度比较
		}).Parse(tmpl)
	})

	if statusTemplate == nil {
		http.Error(w, "模板解析错误", http.StatusInternalServerError)
		return
	}

	if err := statusTemplate.Execute(w, data); err != nil {
		http.Error(w, "模板执行错误: "+err.Error(), http.StatusInternalServerError)
	}
}

// 字节格式化
func FormatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// 带宽格式化
func FormatBytesPerSec(bytes uint64, _ uint64) string {
	return FormatBytes(bytes) + "/s"
}

// 网络流量带宽格式化
func FormatNetworkBandwidth(bytes uint64) string {
	return FormatBytes(bytes) + "/s"
}

func prepareStatusData(r *http.Request) StatusData {
	// 内存统计
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// 获取客户端 IP
	clientIP := GetClientIP(r)

	// 复制 ProxyGroups
	config.CfgMu.RLock()
	proxyGroups := make(map[string]*config.ProxyGroupConfig)
	for name, group := range config.Cfg.ProxyGroups {
		groupCopy := &config.ProxyGroupConfig{
			Proxies:     make([]*config.ProxyConfig, len(group.Proxies)),
			Domains:     group.Domains,
			LoadBalance: group.LoadBalance,
			Stats:       &config.GroupStats{ProxyStats: make(map[string]*config.ProxyStats)},
		}
		for i, p := range group.Proxies {
			groupCopy.Proxies[i] = &config.ProxyConfig{
				Name:   p.Name,
				Type:   p.Type,
				Server: p.Server,
				Port:   0,
				UDP:    p.UDP,
			}
			if group.Stats != nil && group.Stats.ProxyStats != nil {
				if stats, ok := group.Stats.ProxyStats[p.Name]; ok {
					groupCopy.Stats.ProxyStats[p.Name] = stats
				} else {
					groupCopy.Stats.ProxyStats[p.Name] = &config.ProxyStats{}
				}
			} else {
				groupCopy.Stats.ProxyStats[p.Name] = &config.ProxyStats{}
			}
		}
		proxyGroups[name] = groupCopy
	}
	config.CfgMu.RUnlock()

	// 获取系统与应用流量统计（深拷贝）
	trafficStats := GlobalTrafficStats.GetTrafficStats()

	return StatusData{
		Timestamp:     time.Now(),
		Uptime:        time.Since(config.StartTime),
		Version:       config.Version,
		Goroutines:    runtime.NumGoroutine(),
		MemoryStats:   memStats,
		ProxyGroups:   proxyGroups,
		TrafficStats:  trafficStats, // 包含系统统计 + 应用统计
		ClientIP:      clientIP,
		ActiveClients: ActiveClients.GetAll(),
		WebPath:       config.Cfg.Web.Path, // 注入动态 Web.Path
	}
}

func GetClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	if xr := r.Header.Get("X-Real-IP"); xr != "" {
		return xr
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}