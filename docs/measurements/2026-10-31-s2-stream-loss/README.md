# S2 Stream丢失补充验收

测量2026-10-31，整理2026-11-01；5/5有效，源码a00e15101716f21cd674662e74210130719a0bcc。与fdccc71的S2 v5矩阵及20次故障分开归档。

41个原始文件无损压缩，manifest.json列压缩/解压SHA256。attempts.json保留原valid。verification.json逐条核对同机boot/版本/单调时钟和applied计数。runtime日志和恢复结果在raw中。只删除本轮私有Stream，保留checkpoint；验证TTL被动恢复和操作者重建空消费组后新事件恢复，不代表全目录主动重建或历史事件自动补回。

```bash
sha256sum -c SHA256SUMS
python3 scripts/p2/verify_stream_loss.py docs/measurements/2026-10-31-s2-stream-loss --output /tmp/s2-loss-verification.json
cmp /tmp/s2-loss-verification.json docs/measurements/2026-10-31-s2-stream-loss/verification.json
```

完整说明见[报告补充](../../S2跨实例一致性窗口报告-2026-10-29.md)。不包含env、DSN、令牌、二进制或Redis持久化文件。
