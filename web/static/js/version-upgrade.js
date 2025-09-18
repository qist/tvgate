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
        statusInterval = setInterval(async () => {
            try {
                const res = await fetch(window.webPath + "github/status", {
                    method: 'GET',
                    headers: {
                        'Content-Type': 'application/json'
                    }
                });
                const data = await res.json();
                statusDiv.textContent = `状态: ${data.state}, 消息: ${data.message}`;
                if (data.state === "success" || data.state === "error") {
                    clearInterval(statusInterval);
                    statusInterval = null;
                }
            } catch (err) {
                console.error("获取状态失败:", err);
                statusDiv.textContent = `获取状态失败: ${err.message}`;
            }
        }, 2000);
    }

    if (closeModalBtn) {
        closeModalBtn.addEventListener("click", closeModal);
    }

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

                let html = `<p>当前版本: <b>${currentVersion}</b></p><ul>`;
                releases.forEach(r => {
                    const tag = r.tag_name;
                    html += `<li>${tag} <button class="btn upgrade-btn" data-version="${tag}">升级到该版本</button></li>`;
                });
                html += "</ul>";

                openModal(html);

                document.querySelectorAll(".upgrade-btn").forEach(btn => {
                    btn.addEventListener("click", async () => {
                        const version = btn.dataset.version;
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