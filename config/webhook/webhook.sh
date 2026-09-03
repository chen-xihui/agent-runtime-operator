#!/usr/bin/env bash
# 生成 admission webhook 的自签名证书与 caBundle，供集群部署 ValidatingWebhookConfiguration 用。
# 用法：bash config/webhook/webhook.sh <OPERATOR_NODE_IP>
# 说明：operator 以进程方式运行于宿主机（本仓库验证环境），webhook 服务监听 :9443；
#       需把 operator 启动参数加 --enable-webhooks 并在宿主机 /tmp/k8s-webhook-server/serving-certs 放置 tls.crt/tls.key。
#       若 operator 以 Deployment 部署，改用 cert-manager + Service（见 manifest 注释）。
set -euo pipefail
NODE_IP="${1:?usage: $0 <OPERATOR_NODE_IP>}"
OUT="$(dirname "$0")/certs"
mkdir -p "$OUT"

cat > "$OUT/csr.conf" <<EOF
[req]
req_extensions = v3_req
distinguished_name = req_distinguished_name
prompt = no
[req_distinguished_name]
CN = agent-runtime-operator
[v3_req]
subjectAltName = @alt_names
[alt_names]
IP.1 = ${NODE_IP}
DNS.1 = agent-runtime-operator
EOF

# CA
openssl genrsa -out "$OUT/ca.key" 2048 2>/dev/null
openssl req -x509 -new -nodes -key "$OUT/ca.key" -subj "/CN=agent-runtime-ca" -days 365 -out "$OUT/ca.crt" 2>/dev/null
# 服务端证书（operator 进程侧）
openssl genrsa -out "$OUT/tls.key" 2048 2>/dev/null
openssl req -new -key "$OUT/tls.key" -out "$OUT/tls.csr" -config "$OUT/csr.conf" 2>/dev/null
openssl x509 -req -in "$OUT/tls.csr" -CA "$OUT/ca.crt" -CAkey "$OUT/ca.key" \
  -CAcreateserial -out "$OUT/tls.crt" -days 365 -extensions v3_req -extfile "$OUT/csr.conf" 2>/dev/null

echo "=== 服务端证书（放 /tmp/k8s-webhook-server/serving-certs/）==="
echo "cp $OUT/tls.crt $OUT/tls.key /tmp/k8s-webhook-server/serving-certs/"
mkdir -p /tmp/k8s-webhook-server/serving-certs
cp "$OUT/tls.crt" /tmp/k8s-webhook-server/serving-certs/
cp "$OUT/tls.key" /tmp/k8s-webhook-server/serving-certs/

echo "=== caBundle（base64，填入 ValidatingWebhookConfiguration 的 caBundle 字段）==="
base64 -w0 "$OUT/ca.crt"
echo
echo "完成。用生成的 caBundle 更新 config/webhook/validating-webhook.yaml 后 kubectl apply -f"
