"""临时辅助：带密码 sftp 上传到 192.168.0.31 /root/（验证 NATS 闭环用）。
用法: python _vm_upload.py <本地文件> [远端路径(默认 /root/<文件名>)]
"""
import sys
import os
import paramiko

HOST = "192.168.0.31"
USER = "root"
PWD = "q1w2e3r4t5"


def main():
    src = sys.argv[1]
    dst = sys.argv[2] if len(sys.argv) > 2 else "/root/" + os.path.basename(src)
    t = paramiko.Transport((HOST, 22))
    t.connect(username=USER, password=PWD)
    sftp = paramiko.SFTPClient.from_transport(t)
    sftp.put(src, dst)
    st = sftp.stat(dst)
    print(f"uploaded {src} -> {dst} size={st.st_size}")
    sftp.close()
    t.close()


if __name__ == "__main__":
    main()
