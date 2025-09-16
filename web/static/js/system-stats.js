// 更新圆环进度的统一函数
function updateCircleChart(circle, percent) {
    if (!circle) return;
    const radius = circle.r.baseVal.value;
    const circumference = 2 * Math.PI * radius;
    circle.style.strokeDasharray = circumference;
    circle.style.strokeDashoffset = circumference * (1 - percent / 100);
}

// 实时更新系统资源使用率
function updateSystemStats() {
    const monitorPath = window.monitorPath || '/status';
    
    fetch(monitorPath + '?format=json')
        .then(response => response.json())
        .then(data => {
            // ================= CPU =================
            const cpuUsage = Math.round(data.TrafficStats.CPUUsage);
            const cpuChart = document.getElementById('cpuChart');
            const cpuValue = document.getElementById('cpuValue');
            if (cpuChart && cpuValue) {
                updateCircleChart(cpuChart, cpuUsage);
                cpuValue.textContent = `${cpuUsage}%`;
            }

            // ================= 内存 =================
            const memoryTotal = data.TrafficStats.MemoryTotal;
            const memoryUsage = memoryTotal > 0 ? Math.round((data.TrafficStats.MemoryUsage / memoryTotal) * 100) : 0;
            const memoryChart = document.getElementById('memoryChart');
            const memoryValue = document.getElementById('memoryValue');
            const memoryInfo = document.getElementById('memoryInfo');
            if (memoryChart && memoryValue && memoryInfo) {
                updateCircleChart(memoryChart, memoryUsage);
                memoryValue.textContent = `${memoryUsage}%`;
                memoryInfo.textContent = `${formatBytes(data.TrafficStats.MemoryUsage)} / ${formatBytes(memoryTotal)}`;
            }

            // ================= SWAP =================
            const swapTotal = data.TrafficStats.SwapTotal || 0;
            const swapUsage = data.TrafficStats.SwapUsage || 0;
            const swapUsagePercent = swapTotal > 0 ? Math.round((swapUsage / swapTotal) * 100) : 0;
            const swapChart = document.getElementById('swapChart');
            const swapValue = document.getElementById('swapValue');
            const swapInfo = document.getElementById('swapInfo');
            if (swapChart && swapValue && swapInfo) {
                updateCircleChart(swapChart, swapUsagePercent);
                swapValue.textContent = `${swapUsagePercent}%`;
                swapInfo.textContent = `${formatBytes(swapUsage)} / ${formatBytes(swapTotal)}`;
            }

            // ================= 硬盘 =================
            const diskTotal = data.TrafficStats.DiskTotal;
            const diskUsage = data.TrafficStats.DiskUsage;
            const diskUsagePercent = diskTotal > 0 ? Math.round((diskUsage / diskTotal) * 100) : 0;
            const diskChart = document.getElementById('diskChart');
            const diskValue = document.getElementById('diskValue');
            const diskInfo = document.getElementById('diskInfo');
            if (diskChart && diskValue && diskInfo) {
                updateCircleChart(diskChart, diskUsagePercent);
                diskValue.textContent = `${diskUsagePercent}%`;
                diskInfo.textContent = `${formatBytes(diskUsage)} / ${formatBytes(diskTotal)}`;
            }

            // ================= 高使用率变色 =================
            [
                {chart: cpuChart, percent: cpuUsage},
                {chart: memoryChart, percent: memoryUsage},
                {chart: swapChart, percent: swapUsagePercent},
                {chart: diskChart, percent: diskUsagePercent}
            ].forEach(item => {
                if (!item.chart) return;
                if (item.percent > 90) item.chart.classList.add('high-usage');
                else item.chart.classList.remove('high-usage');
            });

            // ================= 存储表格 =================
            const storageTableBody = document.getElementById('storage-table-body');
            if (storageTableBody && data.TrafficStats.DiskPartitions) {
                let storageHTML = '';
                data.TrafficStats.DiskPartitions.forEach(partition => {
                    let usageClass = '';
                    if (partition.UsedPercent > 90) usageClass = 'critical';
                    else if (partition.UsedPercent > 80) usageClass = 'warning';

                    storageHTML += `
                        <tr>
                            <td>${partition.Path}</td>
                            <td>${partition.MountPoint}</td>
                            <td>${partition.FsType}</td>
                            <td>${formatBytes(partition.Used)}</td>
                            <td>${formatBytes(partition.Total)}</td>
                            <td>
                                <div class="usage-bar-container">
                                    <div class="usage-bar ${usageClass}" style="width: ${partition.UsedPercent.toFixed(2)}%"></div>
                                </div>
                                <div>${partition.UsedPercent.toFixed(2)}%</div>
                            </td>
                        </tr>
                    `;
                });
                storageTableBody.innerHTML = storageHTML;
            }

            // ================= 网卡流量 =================
            const networkTableBody = document.getElementById('network-table-body');
            if (networkTableBody && data.TrafficStats.NetworkInterfaces) {
                let networkHTML = '';
                data.TrafficStats.NetworkInterfaces.forEach(netInterface => {
                    networkHTML += `
                        <tr>
                            <td>${netInterface.Name}</td>
                            <td>${formatBytes(netInterface.BytesRecv)}</td>
                            <td>${formatBytes(netInterface.BytesSent)}</td>
                            <td>${formatBytes(netInterface.RecvBandwidth)}/s</td>
                            <td>${formatBytes(netInterface.SendBandwidth)}/s</td>
                        </tr>
                    `;
                });
                networkTableBody.innerHTML = networkHTML;
            }

            // ================= TVGate应用状态 =================
            const appCpuUsageElement = document.getElementById('appCpuUsage');
            const appMemoryUsageElement = document.getElementById('appMemoryUsage');
            const appTotalBytesElement = document.getElementById('appTotalBytes');
            const appInboundBytesElement = document.getElementById('appInboundBytes');
            const appOutboundBytesElement = document.getElementById('appOutboundBytes');

            if (appCpuUsageElement) appCpuUsageElement.textContent = `${data.TrafficStats.App.CPUPercent.toFixed(2)}%`;
            if (appMemoryUsageElement) appMemoryUsageElement.textContent = formatBytes(data.TrafficStats.App.MemoryUsage);
            if (appTotalBytesElement) appTotalBytesElement.textContent = formatBytes(data.TrafficStats.App.TotalBytes);
            if (appInboundBytesElement) appInboundBytesElement.textContent = formatBytes(data.TrafficStats.App.InboundBytes);
            if (appOutboundBytesElement) appOutboundBytesElement.textContent = formatBytes(data.TrafficStats.App.OutboundBytes);

            // ================= 新增系统状态 =================
            const systemLoadElement = document.getElementById('systemLoad');
            const connectionsElement = document.getElementById('connections');
            const inboundBandwidthElement = document.getElementById('inboundBandwidth');
            const outboundBandwidthElement = document.getElementById('outboundBandwidth');
            const inboundBytesElement = document.getElementById('inboundBytes');
            const outboundBytesElement = document.getElementById('outboundBytes');

            if (systemLoadElement && data.TrafficStats.LoadAverage) {
                systemLoadElement.textContent = `${data.TrafficStats.LoadAverage.Load1.toFixed(2)}, ${data.TrafficStats.LoadAverage.Load5.toFixed(2)}, ${data.TrafficStats.LoadAverage.Load15.toFixed(2)}`;
            }
            if (connectionsElement) {
                connectionsElement.textContent = data.ActiveClients ? data.ActiveClients.length : data.TrafficStats.ActiveConnections || 0;
            }
            if (inboundBandwidthElement) inboundBandwidthElement.textContent = `${formatBytes(data.TrafficStats.InboundBandwidth)}/s`;
            if (outboundBandwidthElement) outboundBandwidthElement.textContent = `${formatBytes(data.TrafficStats.OutboundBandwidth)}/s`;
            if (inboundBytesElement) inboundBytesElement.textContent = formatBytes(data.TrafficStats.InboundBytes);
            if (outboundBytesElement) outboundBytesElement.textContent = formatBytes(data.TrafficStats.OutboundBytes);

            // ================= 活跃客户端 =================
            const clientsTableBody = document.getElementById('clients-table-body');
            if (clientsTableBody && data.ActiveClients) {
                let clientsHTML = '';
                data.ActiveClients.forEach(client => {
                    clientsHTML += `
                        <tr>
                            <td>${client.IP}</td>
                            <td title="${client.URL}">${client.URL}</td>
                            <td>${client.ConnectionType}</td>
                            <td title="${client.UserAgent}">${client.UserAgent}</td>
                            <td>${formatTime(client.ConnectedAt)}</td>
                            <td>${formatTime(client.LastActive)}</td>
                        </tr>
                    `;
                });
                clientsTableBody.innerHTML = clientsHTML;
            }

            // ================= 系统信息 =================
            const osElement = document.getElementById('os');
            const kernelVersionElement = document.getElementById('kernelVersion');
            const cpuArchElement = document.getElementById('cpuArch');
            const versionElement = document.getElementById('version');
            const uptimeElement = document.getElementById('uptime');
            const goroutinesElement = document.getElementById('goroutines');
            const clientIPElement = document.getElementById('clientIP');

            if (osElement) osElement.textContent = data.TrafficStats.HostInfo.Platform || '未知';
            if (kernelVersionElement) kernelVersionElement.textContent = data.TrafficStats.HostInfo.KernelVersion || '未知';
            if (cpuArchElement) cpuArchElement.textContent = data.TrafficStats.HostInfo.KernelArch || '未知';
            if (versionElement) versionElement.textContent = data.Version || '未知';
            if (uptimeElement) uptimeElement.textContent = formatUptime(data.Uptime) || '未知';
            if (goroutinesElement) goroutinesElement.textContent = data.Goroutines || '未知';
            if (clientIPElement) clientIPElement.textContent = data.ClientIP || '未知';
        })
        .catch(error => console.error('获取系统状态失败:', error));
}

// ================= 工具函数 =================
function formatBytes(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
}

function formatUptime(uptime) {
    if (!uptime) return '未知';
    const totalSeconds = uptime.Seconds || 0;
    const days = Math.floor(totalSeconds / 86400);
    const hours = Math.floor((totalSeconds % 86400) / 3600);
    const minutes = Math.floor((totalSeconds % 3600) / 60);
    return `${days}天${hours}小时${minutes}分钟`;
}

function formatTime(timeString) {
    if (!timeString) return '未知';
    const date = new Date(timeString);
    return date.toLocaleString('zh-CN', {
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit'
    });
}

// ================= 页面加载后定时更新 =================
document.addEventListener('DOMContentLoaded', function() {
    updateSystemStats();
    setInterval(updateSystemStats, 5000);
});
