import json

from main import decode_paddlex_ocr_payload


def test_decode_paddlex_ocr_payload():
    inner = {
        "result": {
            "ocrResults": [
                {
                    "prunedResult": {
                        "rec_texts": ["50", "00"],
                        "rec_scores": [0.98, 0.96],
                        "rec_polys": [
                            [[0, 0], [1, 0], [1, 1], [0, 1]],
                            [[2, 0], [3, 0], [3, 1], [2, 1]],
                        ],
                    }
                }
            ]
        }
    }
    payload = {
        "outputs": [
            {
                "name": "output",
                "data": [json.dumps(inner, ensure_ascii=False)],
            }
        ]
    }

    text, items = decode_paddlex_ocr_payload(payload)

    assert text == "5000"
    assert len(items) == 2
    assert items[0]["confidence"] == 0.98


if __name__ == "__main__":
    test_decode_paddlex_ocr_payload()
