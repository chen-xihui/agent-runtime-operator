"""临时辅助：带密码 ssh 到 192.168.0.31 执行任意命令（验证 NATS 环境用）。
用法: python _vm_exec.py "远程命令"
"""
import sys
import paramiko

HOST = "192.168.0.31"
USER = "root"
PWD = "q1w2e3r4t5"


def main():
    cmd = sys.argv[1] if len(sys.argv) > 1 else "echo no-cmd"
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(HOST, username=USER, password=PWD, timeout=10)
    _, o, e = c.exec_command(cmd, timeout=120)
    out = o.read().decode(errors="replace")
    err = e.read().decode(errors="replace")
    if out:
        print("[OUT]", out, sep="\n")
    if err:
        print("[ERR]", err, sep="\n", file=sys.stderr)
    rc = o.channel.recv_exit_status()
    print(f"[RC={rc}]")
    c.close()


if __name__ == "__main__":
    main()
