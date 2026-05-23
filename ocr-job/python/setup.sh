#!/usr/bin/env bash
set -e

# RoboMaster OCR Worker 一键部署脚本
# 用法: bash setup.sh [--cpu]

DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DIR"

echo "=== 安装 OCR Worker 依赖 ==="

# 检测 Python
PYTHON=${PYTHON:-python3}
if ! command -v $PYTHON &>/dev/null; then
    echo "错误: 找不到 python3"
    exit 1
fi

echo "Python: $($PYTHON --version)"

# 检测 CUDA
if [[ "$1" == "--cpu" ]] || ! command -v nvidia-smi &>/dev/null; then
    echo "模式: CPU"
    $PYTHON -m pip install torch --index-url https://download.pytorch.org/whl/cpu
else
    echo "模式: GPU (CUDA)"
    CUDA_VERSION=$(nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null | head -1)
    echo "CUDA驱动: $CUDA_VERSION"
    # PyTorch会自动安装CUDA版
fi

# 安装依赖
$PYTHON -m pip install -r requirements.txt

# 下载EasyOCR模型
echo "=== 下载 EasyOCR 模型 ==="
$PYTHON -c "
import os, urllib.request, zipfile

models = {
    'craft_mlt_25k.zip': 'https://github.com/JaidedAI/EasyOCR/releases/download/pre-v1.1.6/craft_mlt_25k.zip',
    'zh_sim_g2.zip': 'https://github.com/JaidedAI/EasyOCR/releases/download/v1.3/zh_sim_g2.zip',
    'latin_g2.zip': 'https://github.com/JaidedAI/EasyOCR/releases/download/v1.3/latin_g2.zip',
}

mdl_dir = os.path.expanduser('~/.EasyOCR/model')
os.makedirs(mdl_dir, exist_ok=True)

for name, url in models.items():
    path = os.path.join(mdl_dir, name)
    if os.path.exists(path):
        print(f'[跳过] {name}')
        continue
    print(f'[下载] {name}...')
    urllib.request.urlretrieve(url, path)
    with zipfile.ZipFile(path) as z:
        z.extractall(mdl_dir)
    print(f'[完成] {name}')

print('EasyOCR 模型就绪')
"

# 验证
echo "=== 验证 ==="
$PYTHON -c "
from ocr_engine import ocr_settlement
import cv2
print('OCR 引擎加载成功')
print('API: ocr_settlement(img: np.ndarray) -> str')
"

echo "=== 部署完成 ==="
echo "使用方式:"
echo "  python3 -c \"from ocr_engine import ocr_settlement; import cv2; print(ocr_settlement(cv2.imread('screenshot.png')))\""