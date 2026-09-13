#!/usr/bin/env python3
"""Real CLI/Linux PTYs against signed API and PostgreSQL; retained telemetry is synthetic."""
import copy
import json
import os
from pathlib import Path
import subprocess
import tempfile
import urllib.request

from authenticated import ProtectedTerminal
from release_review import ReleaseProxy
from review import OPENER


def main():
    api = os.environ['CONDUCTOR_API_URL']
    control = os.environ['CONDUCTOR_TERMINAL_CONTROL_URL']
    binary = os.environ['CONDUCTOR_BIN']
    files = {name: Path(path) for name, path in json.loads(os.environ['CONDUCTOR_TERMINAL_CREDENTIAL_FILES']).items()}
    tokens = {name: path.read_text().strip() for name, path in files.items()}
    setup = json.loads(os.environ['CONDUCTOR_TERMINAL_RUNTIME_SETUP'])
    record = setup['record']
    proxy = ReleaseProxy(api, tokens)
    proxy.drop_path = '/api/v1/runtime-evidence'
    terminals = []

    def cli(identity, args, expect=0, repository='application'):
        env = dict(os.environ)
        for name in ['CONDUCTOR_TOKEN', 'CONDUCTOR_TOKEN_FILE', 'CONDUCTOR_ACTOR', 'CONDUCTOR_WORKSPACE', 'CONDUCTOR_REPOSITORY_ID']:
            env.pop(name, None)
        env.update(CONDUCTOR_URL=proxy.url, CONDUCTOR_TOKEN_FILE=str(files[identity]))
        result = subprocess.run([binary, args[0], '--workspace', 'team', '--repository-id', repository] + args[1:],
                                env=env, capture_output=True, timeout=15)
        for token in tokens.values():
            assert token.encode() not in result.stdout + result.stderr
        assert result.returncode == expect, repr(result.stdout + result.stderr)
        return json.loads(result.stdout) if result.returncode == 0 else None

    def launch(identity, identifier=None):
        args = ['--workspace', 'team', '--repository-id', 'application', '--view', 'runtime']
        if identifier:
            args.append(identifier)
        term = ProtectedTerminal(binary, proxy.url, args, files[identity], tokens)
        terminals.append(term)
        term.wait_text('Shared record inspected' if identifier else 'Shared runtime loaded')
        term.settle()
        return term

    def import_file(term, path):
        start = len(term.output)
        term.send('c')
        term.wait_text('Request JSON file path', start)
        term.send(str(path) + '\r')
        term.wait_text('Request preview loaded', start)
        term.settle()

    def confirm(term, expected):
        start = len(term.output)
        term.send('s')
        term.wait_text('Type collect-runtime', start)
        term.send('collect-runtime\r')
        term.wait_text(expected, start)
        term.settle()

    def operator(action):
        with OPENER.open(urllib.request.Request(control + '/' + action, method='POST'), timeout=10) as response:
            assert response.status == 200

    try:
        with tempfile.TemporaryDirectory(prefix='conductor-runtime-pty-') as name:
            directory = Path(name)
            request = {'idempotencyKey': 'cli-runtime-captured', 'input': setup['input']}
            path = directory / 'request.json'
            path.write_text(json.dumps(request))
            before = len(proxy.snapshot())
            preview = cli('agent', ['runtime-preview', '--file', str(path)])
            assert len(proxy.snapshot()) == before, 'offline preview contacted API'
            proxy.drop_next_create = True
            cli('agent', ['runtime-collect', '--file', str(path), '--digest', preview['digest']], expect=1)
            created = cli('agent', ['runtime-collect', '--file', str(path), '--digest', preview['digest']])
            calls = proxy.snapshot()[before:]
            assert len(calls) == 2 and all(call['method'] == 'POST' for call in calls)
            assert calls[0]['body'] == calls[1]['body'] == setup['input']
            assert calls[0]['key'] == calls[1]['key'] == request['idempotencyKey']
            assert created['requesterId'] == 'person-agent'
            changed = copy.deepcopy(request)
            changed['input']['commit'] = 'f' * 40
            path.write_text(json.dumps(changed))
            before = len(proxy.snapshot())
            cli('agent', ['runtime-collect', '--file', str(path), '--digest', preview['digest']], expect=1)
            assert len(proxy.snapshot()) == before
            page = cli('reader', ['runtime-evidence', '--limit', '1'])
            assert len(page['evidence']) == 1 and page['nextBefore']
            read = cli('reader', ['runtime', record['id']])
            assert read['receipt']['digest'] == record['receipt']['digest']
            cli('reviewer', ['runtime', record['id']], expect=1, repository='private')
            cli('reader', ['runtime-collect', '--file', str(path), '--digest', cli('reader', ['runtime-preview', '--file', str(path)])['digest']], expect=1)

            reviewer = launch('reviewer', record['id'])
            for state in ['latency: met', 'minimum: not_met', 'missing: not_verified']:
                reviewer.wait_text(state)
            reviewer.wait(lambda: '| stale;' in reviewer.text(), 'historical window did not become stale', timeout=20)
            assert 'latency: met' in reviewer.text(), 'aging replaced retained criterion result'
            # Scroll through retained JSON so complete escaped provider text is inspected.
            for _ in range(35):
                reviewer.send('\x1b[6~')
                reviewer.settle()
                if 'runtime-source-tail' in reviewer.text():
                    break
            assert 'runtime-source-tail' in reviewer.text()
            assert 'truncated' in reviewer.text()
            readonly = launch('reader', record['id'])
            before = len(proxy.snapshot())
            readonly.send('c')
            readonly.wait_text('Action blocked:')
            assert len(proxy.snapshot()) == before

            agent = launch('agent')
            request['idempotencyKey'] = 'pty-runtime-captured'
            path.write_text(json.dumps(request))
            import_file(agent, path)
            before = len(proxy.snapshot())
            proxy.drop_next_create = True
            confirm(agent, 'Write not confirmed')
            confirm(agent, 'Runtime collection requested')
            writes = proxy.snapshot()[before:]
            assert len(writes) == 2 and all(item['method'] == 'POST' for item in writes)
            assert writes[0]['key'] == writes[1]['key'] == request['idempotencyKey']
            assert writes[0]['body'] == writes[1]['body'] == request['input']
            before = len(proxy.snapshot())
            agent.send('a')
            agent.settle()
            assert len(proxy.snapshot()) == before, 'runtime view acquired authorization control'

            # Revocation clears retained source, previews and capabilities. Recovery
            # uses the original in-memory token even if its file changes externally.
            files['reviewer'].write_text(tokens['agent'] + '\n')
            operator('revoke')
            start = len(reviewer.output)
            reviewer.send('r')
            reviewer.wait_text('Access unavailable:', start)
            assert 'runtime-source-tail' not in reviewer.text(start)
            assert 'Historical criterion' not in reviewer.text(start)
            operator('restore')
            start = len(reviewer.output)
            reviewer.send('r')
            reviewer.wait_text('Shared record inspected', start)
            assert proxy.snapshot()[-1]['identity'] == 'reviewer'
            files['reviewer'].write_text(tokens['reviewer'] + '\n')
            for terminal in terminals:
                assert b'\x1b]52;c;YQ==' not in terminal.output
                for token in tokens.values():
                    assert token.encode() not in terminal.output
            assert all(item['workspace'] == 'team' and not item['localActor'] for item in proxy.snapshot())
            print('PASS runtime CLI and actual PTY: strict offline preview, captured retries, scoped lists/reads, historical met/not_met/not_verified and aging, complete escaped signals, read-only and agent authority, revocation clearing and fixed-token recovery')
    finally:
        for terminal in terminals:
            terminal.close()
        proxy.close()

if __name__ == '__main__':
    main()
