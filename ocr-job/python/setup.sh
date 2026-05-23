#!/usr/bin/env bash
set -e

DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DIR"

echo "=== RM OCR Worker 一键部署 ==="
echo ""

HAVE_CONDA=false
if command -v conda &>/dev/null; then
    HAVE_CONDA=true
    echo "[conda] $(conda --version)"
else
    echo "[提示] 未检测到 conda，将使用 pip 安装"
fi

ENV_NAME="rm-ocr"
if $HAVE_CONDA; then
    if conda env list | grep -q "^$ENV_NAME "; then
        echo "[conda] 环境 $ENV_NAME 已存在"
    else
        echo "[conda] 创建环境 $ENV_NAME..."
        conda env create -f environment.yml
    fi
    PYTHON="conda run -n $ENV_NAME python"
else
    PYTHON=${PYTHON:-python3}
    $PYTHON -m pip install -r requirements.txt
fi

GPU=true
if [[ "$1" == "--cpu" ]] || ! command -v nvidia-smi &>/dev/null; then
    GPU=false
fi

if $GPU; then
    echo "[torch] GPU 模式"
    if $HAVE_CONDA; then
        conda install -n $ENV_NAME -y pytorch torchvision pytorch-cuda=12.1 -c pytorch -c nvidia
    else
        $PYTHON -m pip install torch torchvision --index-url https://download.pytorch.org/whl/cu121
    fi
else
    echo "[torch] CPU 模式"
    if $HAVE_CONDA; then
        conda install -n $ENV_NAME -y pytorch torchvision cpuonly -c pytorch
    else
        $PYTHON -m pip install torch torchvision --index-url https://download.pytorch.org/whl/cpu
    fi
fi

echo ""
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
    os.remove(path)
    print(f'[完成] {name}')
print('EasyOCR 模型就绪')
"

echo ""
echo "=== 验证 ==="
$PYTHON -c "
from ocr_engine import ocr_settlement
import cv2
print('✅ OCR 引擎加载成功')
"

echo ""
echo "=== 部署完成 ==="
echo ""
echo "用法:"
echo "  conda activate $ENV_NAME"
echo '  python -c "from ocr_engine import ocr_settlement; import cv2; print(ocr_settlement(cv2.imread(\"screenshot.png\")))"'
