#!/bin/bash

# 복사할 대상 서버 정보
TARGET_USER="root"
TARGET_HOST="10.1.1.2"
TARGET_PATH="/root/workspace/instorage-container-processor"

sshpass -p '1234' scp -r bin scripts ${TARGET_USER}@${TARGET_HOST}:${TARGET_PATH}

echo "Copy completed!"