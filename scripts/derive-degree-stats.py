#!/usr/bin/env python3
"""P1: derive degree distributions from public graphs; only histograms enter the repository.

Alibaba MSCallGraph columns: ,traceid,timestamp,rpcid,um,rpctype,dm,interface,rt.
Service dependencies use rpc/http/mq calls; storage usage uses db/mc calls. Distinct
(caller, callee, kind) pairs are counted once; self calls and UNKNOWN/UNAVAILABLE ids are dropped.
SNAP web-Google is a web link graph; its histogram is a shape reference only, never a CMDB.
"""
import argparse, collections, gzip, hashlib, json, tarfile
from pathlib import Path

SERVICE_TYPES = {'rpc', 'http', 'mq'}
STORAGE_TYPES = {'db', 'mc'}


def sha(path):
    h = hashlib.sha256()
    with path.open('rb') as f:
        for b in iter(lambda: f.read(1 << 20), b''):
            h.update(b)
    return h.hexdigest()


def histogram(counter, nodes):
    hist = collections.Counter(counter.get(n, 0) for n in nodes)
    return [[d, hist[d]] for d in sorted(hist)]


def alibaba(paths):
    service, storage = set(), set()
    rows = dropped = 0
    for path in paths:
        with tarfile.open(path, 'r|gz') as archive:
            for member in archive:
                if not member.isfile():
                    continue
                f = archive.extractfile(member)
                f.readline()
                for line in f:
                    p = line.decode().rstrip('\n').split(',')
                    um, kind, dm = p[4], p[5], p[6]
                    rows += 1
                    if not um or not dm or um.startswith(('UNKNOWN', 'UNAVAIL')) or dm.startswith(('UNKNOWN', 'UNAVAIL')) or um == dm:
                        dropped += 1
                        continue
                    if kind in SERVICE_TYPES:
                        service.add((um, dm))
                    elif kind in STORAGE_TYPES:
                        storage.add((um, dm))
    # Services are nodes that appear on either side of a service call or call storage.
    nodes = {u for u, _ in service} | {d for _, d in service} | {u for u, _ in storage}
    storage_nodes = {d for _, d in storage} - nodes
    out = collections.Counter(u for u, _ in service)
    inn = collections.Counter(d for _, d in service)
    uses = collections.Counter(u for u, _ in storage)
    used_by = collections.Counter(d for _, d in storage)
    return {
        'source': 'public', 'meaning': 'sampled microservice call graph, not CMDB',
        'rows_read': rows, 'rows_dropped': dropped,
        'services': len(nodes), 'service_edges': len(service),
        'storage_nodes': len(storage_nodes), 'storage_edges': len(storage),
        'service_out_degree': histogram(out, nodes),
        'service_in_degree': histogram(inn, nodes),
        'service_storage_out_degree': histogram(uses, nodes),
        'storage_in_degree': histogram(used_by, storage_nodes),
    }


def snap(path):
    out, inn, nodes, edges = collections.Counter(), collections.Counter(), set(), 0
    with gzip.open(path, 'rt') as f:
        for line in f:
            if line.startswith('#'):
                continue
            a, b = line.split()
            out[a] += 1
            inn[b] += 1
            nodes.update((a, b))
            edges += 1
    return {'source': 'public', 'meaning': 'web link graph, not CMDB; shape reference only',
            'nodes': len(nodes), 'edges': edges,
            'out_degree': histogram(out, nodes), 'in_degree': histogram(inn, nodes)}


if __name__ == '__main__':
    p = argparse.ArgumentParser()
    p.add_argument('--data-dir', type=Path, required=True)
    p.add_argument('--output', type=Path, default=Path('datasets/stats/graph-degree.json'))
    a = p.parse_args()
    ali = [a.data_dir / 'MSCallGraph_0.tar.gz', a.data_dir / 'MSCallGraph_1.tar.gz']
    web = a.data_dir / 'web-Google.txt.gz'
    result = {
        'generator': 'scripts/derive-degree-stats.py',
        'inputs': {p.name: sha(p) for p in ali + [web]},
        'alibaba': alibaba(ali),
        'snap_web_google': snap(web),
    }
    a.output.parent.mkdir(parents=True, exist_ok=True)
    a.output.write_text(json.dumps(result, indent=1) + '\n')
    s = result['alibaba']
    print(json.dumps({k: s[k] for k in ('services', 'service_edges', 'storage_nodes', 'storage_edges')}))
