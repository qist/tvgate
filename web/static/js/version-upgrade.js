document.addEventListener("DOMContentLoaded", () => {
    const currentVersion = document.getElementById("currentVersion")?.textContent || "unknown";
    const checkUpdateBtn = document.getElementById("showUpdateModal");
    const updateModal = document.getElementById("updateModal");
    const updateContent = document.getElementById("releaseList");
    const closeModalBtn = document.getElementById("closeUpdateModal");

    let statusInterval = null;
    let restartRetryCount = 0;
    const restartMaxRetries = 120;
    const storedTargetVersionKey = "tvgate_upgrade_target_version";
    try {
        const target = localStorage.getItem(storedTargetVersionKey);
        if (target && target === currentVersion) {
            localStorage.removeItem(storedTargetVersionKey);
        }
    } catch (e) {}

    function openModal(html) {
        updateContent.innerHTML = html;
        updateModal.style.display = "block";
    }

    function closeModal() {
        updateModal.style.display = "none";
        if (statusInterval) {
            clearInterval(statusInterval);
            statusInterval = null;
        }
    }

    async function startStatusPolling(targetVersion) {
        const statusDiv = document.getElementById("updateStatusLog");
        if (statusInterval) clearInterval(statusInterval);
        restartRetryCount = 0;
        if (targetVersion) {
            try {
                localStorage.setItem(storedTargetVersionKey, targetVersion);
            } catch (e) {}
        }
        
        // 添加进度条元素
        let progressBar = document.getElementById("updateProgressBar");
        if (!progressBar) {
            progressBar = document.createElement("div");
            progressBar.id = "updateProgressBar";
            progressBar.innerHTML = `
                <div style="margin: 10px 0;">
                    <div style="background-color: #f0f0f0; border-radius: 10px; overflow: hidden;">
                        <div id="progressFill" style="width: 0%; height: 20px; background-color: #4CAF50; 
                             text-align: center; line-height: 20px; color: white; font-size: 12px; transition: width 0.3s;">
                            0%
                        </div>
                    </div>
                </div>
            `;
            statusDiv.parentNode.insertBefore(progressBar, statusDiv.nextSibling);
        }
        
        const progressFill = document.getElementById("progressFill");
        
        statusInterval = setInterval(async () => {
            try {
                const res = await fetch(window.webPath + "github/status", {
                    method: 'GET',
                    headers: {
                        'Content-Type': 'application/json'
                    }
                });
                const data = await res.json();
                
                // 更新状态文本
                const target = data.target_version || (() => {
                    try { return localStorage.getItem(storedTargetVersionKey) || ""; } catch (e) { return ""; }
                })();
                const versionText = data.version ? `, 当前版本: ${data.version}` : "";
                const targetText = target ? `, 目标版本: ${target}` : "";
                statusDiv.textContent = `状态: ${data.state}, 消息: ${data.message}${versionText}${targetText}`;
                
                // 根据状态更新进度条
                let progress = 0;
                switch(data.state) {
                    case "starting":
                        progress = 10;
                        break;
                    case "downloading":
                        progress = 30;
                        break;
                    case "backing_up":
                        progress = 50;
                        break;
                    case "unzipping":
                        progress = 70;
                        break;
                    case "restarting":
                        progress = 90;
                        break;
                    case "success":
                        progress = 100;
                        break;
                    case "error":
                        progress = 0;
                        progressFill.style.backgroundColor = "#F44336"; // 错误时变红色
                        break;
                    default:
                        progress = 0;
                }
                
                progressFill.style.width = progress + "%";
                progressFill.textContent = progress + "%";
                
                if (data.state === "error") {
                    clearInterval(statusInterval);
                    statusInterval = null;
                    
                    // 成功或失败后显示提示
                    setTimeout(() => {
                        closeModal();
                    }, 3000);
                    return;
                }

                if (data.state === "restarting" || data.state === "success") {
                    const targetNow = data.target_version || (() => {
                        try { return localStorage.getItem(storedTargetVersionKey) || ""; } catch (e) { return ""; }
                    })();

                    if (targetNow && data.version && data.version === targetNow) {
                        try { localStorage.removeItem(storedTargetVersionKey); } catch (e) {}
                        clearInterval(statusInterval);
                        statusInterval = null;
                        setTimeout(() => location.reload(), 800);
                        return;
                    }

                    statusDiv.textContent = `状态: restarting, 消息: 正在重启中，等待新版本启动...${versionText}${targetText}`;
                }
            } catch (err) {
                console.error("获取状态失败:", err);
                const target = (() => {
                    try { return localStorage.getItem(storedTargetVersionKey) || ""; } catch (e) { return ""; }
                })();
                const isWaitingRestart = !!target;
                if (!isWaitingRestart) {
                    statusDiv.textContent = `获取状态失败: ${err.message}`;
                    progressFill.style.width = "0%";
                    progressFill.textContent = "0%";
                    progressFill.style.backgroundColor = "#F44336";
                    setTimeout(() => {
                        closeModal();
                    }, 3000);
                    return;
                }

                restartRetryCount++;
                progressFill.style.width = "90%";
                progressFill.textContent = "90%";
                statusDiv.textContent = `状态: restarting, 消息: 服务重启中，正在等待连接恢复... (${restartRetryCount}/${restartMaxRetries})`;

                if (restartRetryCount >= restartMaxRetries) {
                    clearInterval(statusInterval);
                    statusInterval = null;
                    try { localStorage.removeItem(storedTargetVersionKey); } catch (e) {}
                    statusDiv.textContent = "等待重启超时，请手动刷新页面。";
                }
            }
        }, 1000);
    }

    // 点击版本号检查更新
    const currentVersionElement = document.getElementById("currentVersion");
    if (currentVersionElement) {
        currentVersionElement.addEventListener("click", async () => {
            try {
                const res = await fetch(window.webPath + "github/releases", {
                    method: 'GET',
                    headers: {
                        'Content-Type': 'application/json'
                    }
                });
                if (!res.ok) {
                    const errorMsg = await res.text();
                    throw new Error(`HTTP ${res.status}: ${errorMsg || res.statusText}`);
                }
                const releases = await res.json();

                let html = `<p>当前版本: <b>${currentVersion}</b></p>
                           <div style="display: grid; grid-template-columns: repeat(6, 1fr); gap: 10px; margin-top: 15px;">`;
                releases.forEach((r, index) => {
                    const tag = r.tag_name;
                    const bgColor = (index + 1) % 2 === 0 ? '#4CAF50' : '#2196F3'; // 双数绿色，单数蓝色
                    html += `<div class="version-item" data-version="${tag}" style="background-color: ${bgColor}; padding: 15px 5px; border-radius: 5px; text-align: center; color: white; cursor: pointer; font-weight: bold;">
                                ${tag}
                             </div>`;
                });
                html += "</div>";

                openModal(html);

                // 为每个版本号添加点击事件
                document.querySelectorAll(".version-item").forEach(item => {
                    item.addEventListener("click", async () => {
                        const version = item.dataset.version;
                        if (!confirm(`确定要升级到 ${version} 吗？`)) return;

                        const resp = await fetch(window.webPath + "github/update", {
                            method: "POST",
                            headers: {
                                "Content-Type": "application/json"
                            },
                            body: JSON.stringify({ version })
                        });

                        const data = await resp.json();
                        openModal(`<p>${data.message}</p><div id="updateStatusLog">状态: running, 正在启动升级...</div>`);
                        startStatusPolling(version);
                    });
                });
            } catch (err) {
                console.error("获取版本失败:", err);
                openModal(`<p style="color:red;">错误: ${err.message}</p><p>请检查网络连接或稍后重试。</p>`);
            }
        });
    }

    if (closeModalBtn) {
        closeModalBtn.addEventListener("click", closeModal);
    }

    // 保留按钮点击事件（如果需要）
    if (checkUpdateBtn) {
        checkUpdateBtn.addEventListener("click", async () => {
            try {
                const res = await fetch(window.webPath + "github/releases", {
                    method: 'GET',
                    headers: {
                        'Content-Type': 'application/json'
                    }
                });
                if (!res.ok) {
                    const errorMsg = await res.text();
                    throw new Error(`HTTP ${res.status}: ${errorMsg || res.statusText}`);
                }
                const releases = await res.json();

                let html = `<p>当前版本: <b>${currentVersion}</b></p>
                           <div style="display: grid; grid-template-columns: repeat(6, 1fr); gap: 10px; margin-top: 15px;">`;
                releases.forEach((r, index) => {
                    const tag = r.tag_name;
                    const bgColor = (index + 1) % 2 === 0 ? '#4CAF50' : '#2196F3'; // 双数绿色，单数蓝色
                    html += `<div class="version-item" data-version="${tag}" style="background-color: ${bgColor}; padding: 15px 5px; border-radius: 5px; text-align: center; color: white; cursor: pointer; font-weight: bold;">
                                ${tag}
                             </div>`;
                });
                html += "</div>";

                openModal(html);

                // 为每个版本号添加点击事件
                document.querySelectorAll(".version-item").forEach(item => {
                    item.addEventListener("click", async () => {
                        const version = item.dataset.version;
                        if (!confirm(`确定要升级到 ${version} 吗？`)) return;

                        const resp = await fetch(window.webPath + "github/update", {
                            method: "POST",
                            headers: {
                                "Content-Type": "application/json"
                            },
                            body: JSON.stringify({ version })
                        });

                        const data = await resp.json();
                        openModal(`<p>${data.message}</p><div id="updateStatusLog">状态: running, 正在启动升级...</div>`);
                        startStatusPolling(version);
                    });
                });
            } catch (err) {
                console.error("获取版本失败:", err);
                openModal(`<p style="color:red;">错误: ${err.message}</p><p>请检查网络连接或稍后重试。</p>`);
            }
        });
    }
});
