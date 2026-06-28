# Local-demo data plane: mock ArgoCD API + Loki/VictoriaMetrics data generators.
# Pure Python stdlib — no pip dependencies.
FROM python:3.12-alpine
WORKDIR /app
COPY demo.py .
EXPOSE 8081
ENTRYPOINT ["python", "-u", "demo.py"]
