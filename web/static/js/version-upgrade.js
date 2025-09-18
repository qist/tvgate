document.addEventListener("DOMContentLoaded", () => {
    const currentVersion = document.getElementById("currentVersion")?.textContent || "unknown";
    const checkUpdateBtn = document.getElementById("showUpdateModal");
    const updateModal = document.getElementById("updateModal");
    const updateContent = document.getElementById("releaseList");
    const closeModalBtn = document.getElementById("closeUpdateModal");

    let statusInterval = null;

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

    async function startStatusPolling() {
        const statusDiv = document.getElementById("updateStatusLog");
        if (statusInterval) clearInterval(statusInterval);
        
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
                statusDiv.textContent = `状态: ${data.state}, 消息: ${data.message}`;
                
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
                
                if (data.state === "success" || data.state === "error") {
                    clearInterval(statusInterval);
                    statusInterval = null;
                    
                    // 成功或失败后显示提示
                    if (data.state === "success") {
                        setTimeout(() => {
                            alert("升级成功！页面将重新加载。");
                            location.reload();
                        }, 2000);
                    } else if (data.state === "error") {
                        // 错误情况下3秒后自动关闭弹窗
                        setTimeout(() => {
                            closeModal();
                        }, 3000);
                    }
                }
            } catch (err) {
                console.error("获取状态失败:", err);
                statusDiv.textContent = `获取状态失败: ${err.message}`;
                progressFill.style.width = "0%";
                progressFill.textContent = "0%";
                progressFill.style.backgroundColor = "#F44336";
                
                // 错误情况下3秒后自动关闭弹窗
                setTimeout(() => {
                    closeModal();
                }, 3000);
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
                        startStatusPolling();
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
                        startStatusPolling();
                    });
                });
            } catch (err) {
                console.error("获取版本失败:", err);
                openModal(`<p style="color:red;">错误: ${err.message}</p><p>请检查网络连接或稍后重试。</p>`);
            }
        });
    }
});