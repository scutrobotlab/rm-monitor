import json, os, re
import cv2
import numpy as np

try:
    import pytesseract
    PYTESSERACT = True
except ImportError:
    PYTESSERACT = False

EASYOCR = False
try:
    import easyocr
    EASYOCR = True
except ImportError:
    pass


REF_W, REF_H = 3074, 1730
TABLE = [
    ('经济总量',    'red_economy',    'blue_economy'),
    ('总伤害量',    'red_damage',     'blue_damage'),
    ('击杀对手',    'red_kills',      'blue_kills'),
    ('大/小弹丸数', 'red_ammo',       'blue_ammo'),
]


def _load_rois(path=None):
    if not path:
        path = os.path.join(os.path.dirname(__file__), 'roi_template.json')
    with open(path) as f:
        data = json.load(f)
    rw = data.get('ref_size', {}).get('width', REF_W)
    rh = data.get('ref_size', {}).get('height', REF_H)
    rois = []
    for r in data['rois']:
        if 'x1_norm' in r:
            rois.append((r['name'], r['x1_norm'], r['y1_norm'], r['x2_norm'], r['y2_norm'], r.get('phase', 'always')))
        else:
            rois.append((r['name'], r['x1'] / rw, r['y1'] / rh, r['x2'] / rw, r['y2'] / rh, r.get('phase', 'always')))
    return rois


_ROIS = _load_rois()


def _init_engine():
    if EASYOCR:
        try:
            for k in list(os.environ):
                if 'proxy' in k.lower():
                    os.environ.pop(k)
            import urllib.request
            urllib.request.install_opener(urllib.request.build_opener(urllib.request.ProxyHandler({})))
            gpu = os.environ.get('EASYOCR_GPU', '').lower() in ('1', 'true', 'yes', 'on')
            reader = easyocr.Reader(['ch_sim', 'en'], gpu=gpu, verbose=False)
            return reader, 'easyocr'
        except Exception:
            pass
    if PYTESSERACT:
        cmd = '/tmp/tesseract-extract/usr/bin/tesseract'
        if os.path.exists(cmd):
            pytesseract.pytesseract.tesseract_cmd = cmd
            os.environ['TESSDATA_PREFIX'] = '/tmp/tesseract-extract/usr/share/tesseract-ocr/4.00/tessdata'
    return None, 'tesseract'


_READER, _ENGINE = _init_engine()


def _ocr(img):
    if _ENGINE == 'easyocr':
        r = _READER.readtext(img, paragraph=True)
        t = ' '.join(t for _, t in r)
    else:
        gray = cv2.cvtColor(img, cv2.COLOR_BGR2GRAY)
        up = cv2.resize(gray, None, fx=3, fy=3, interpolation=cv2.INTER_CUBIC)
        clahe = cv2.createCLAHE(clipLimit=3.0, tileGridSize=(8, 8))
        inv = 255 - clahe.apply(up)
        _, bin = cv2.threshold(inv, 0, 255, cv2.THRESH_BINARY + cv2.THRESH_OTSU)
        t = pytesseract.image_to_string(bin, lang='chi_sim+eng', config='--psm 7 --oem 3').strip()
    return t.replace('O', '0').replace('o', '0')


def _detect_settlement(img):
    hsv = cv2.cvtColor(img, cv2.COLOR_BGR2HSV)
    r1 = cv2.inRange(hsv, (0, 30, 30), (10, 255, 255))
    r2 = cv2.inRange(hsv, (160, 30, 30), (180, 255, 255))
    red = cv2.bitwise_or(r1, r2)
    blue = cv2.inRange(hsv, (100, 30, 30), (130, 255, 255))
    tp = img.shape[0] * img.shape[1]
    rp = cv2.countNonZero(red) / tp
    bp = cv2.countNonZero(blue) / tp
    return rp + bp >= 0.15


def _apply(img, phases):
    h, w = img.shape[:2]
    r = {}
    for name, nx, ny, nx2, ny2, phase in _ROIS:
        if phase not in phases:
            continue
        x1, y1 = int(nx * w), int(ny * h)
        x2, y2 = int(nx2 * w), int(ny2 * h)
        crop = img[y1:y2, x1:x2]
        if crop.size == 0:
            continue
        r[name] = _ocr(crop)
    return r


def read_settlement(img: np.ndarray) -> tuple[bool, dict, str]:
    if not _detect_settlement(img):
        return False, {}, ''

    r1 = _apply(img, ('always', 'outpost'))
    ra = r1.get('red_outpost', '').strip().isdigit()
    ba = r1.get('blue_outpost', '').strip().isdigit()
    r2 = _apply(img, ('base',)) if not ra or not ba else {}

    out = {k: v for k, v in r1.items() if 'outpost' not in k}
    out['red_outpost'] = r1.get('red_outpost', '') if ra else '0'
    out['blue_outpost'] = r1.get('blue_outpost', '') if ba else '0'
    out['red_base'] = r2.get('red_base', '') if not ra else ''
    out['blue_base'] = r2.get('blue_base', '') if not ba else ''

    # 验证: 必须有队名+至少一种血量+胜利条件, 才认为是有效战报
    has_teams = bool(out.get('red_team_name', '')) and bool(out.get('blue_team_name', ''))
    has_hp = bool(out.get('red_base', '') or ra) and bool(out.get('blue_base', '') or ba)
    has_victory = bool(out.get('victory_cond', ''))
    if not (has_teams and has_hp and has_victory):
        return False, out, ''

    lines = ['=== 比赛结算数据 ===', '',
             f"红方: {out.get('red_team_name', '')}",
             f"蓝方: {out.get('blue_team_name', '')}", '']

    ro = out.get('red_outpost', '0')
    bo = out.get('blue_outpost', '0')
    if ra and ba:
        lines.append(f"前哨站血量:  红 {ro}  /  蓝 {bo}")
    elif ra and not ba:
        lines.append(f"前哨站血量:  红 {ro}")
        lines.append(f"基地血量:    蓝 {out.get('blue_base', '')}")
    elif not ra and ba:
        lines.append(f"基地血量:    红 {out.get('red_base', '')}")
        lines.append(f"前哨站血量:  蓝 {bo}")
    else:
        lines.append(f"基地血量:    红 {out.get('red_base', '')}  /  蓝 {out.get('blue_base', '')}")
    lines.append(f"胜利判定: {out.get('victory_cond', '')}")
    lines.append('')

    def _ratio(t):
        m = re.match(r'(\d+)\s*/\s*(\d+)', t.strip())
        return (m.group(1), m.group(2)) if m else (t, '')
    ar = _ratio(out.get('red_ammo', ''))
    ab = _ratio(out.get('blue_ammo', ''))

    lines.append(f"{'指标':<12} {'红方':<12} {'蓝方':<12}")
    lines.append('─' * 36)
    for label, rk, bk in TABLE:
        rv, bv = out.get(rk, ''), out.get(bk, '')
        if '弹丸' in label:
            rv = f'{ar[0]}大/{ar[1]}小' if ar[1] else rv
            bv = f'{ab[0]}大/{ab[1]}小' if ab[1] else bv
        lines.append(f"{label:<12} {rv:<12} {bv:<12}")

    return True, out, '\n'.join(lines)


def ocr_settlement(img: np.ndarray) -> tuple[bool, str]:
    ok, _, report = read_settlement(img)
    return ok, report
