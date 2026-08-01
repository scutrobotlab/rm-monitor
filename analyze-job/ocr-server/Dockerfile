FROM ccr-2vdh3abv-pub.cnc.bj.baidubce.com/paddlex/hps:paddlex3.5-cpu

WORKDIR /app

COPY analyze-job/ocr-server/image/server/ /app/

ENV PADDLEX_HPS_DEVICE_TYPE=cpu \
    PADDLEX_HPS_LOGGING_LEVEL=INFO

EXPOSE 8000 8001 8002

CMD ["bash", "/app/server.sh"]
