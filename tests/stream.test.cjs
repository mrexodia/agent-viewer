const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

function environment() {
    const elements = new Map();
    function element() {
        return {
            style: {}, children: [], dataset: {}, textContent: '',
            scrollTop: 0, scrollHeight: 0, clientHeight: 0,
            classList: { add() {}, remove() {} },
            set innerHTML(value) { this.html = value; this.children = []; },
            get innerHTML() { return this.html || this.textContent; },
            appendChild(child) { this.children.push(child); },
            addEventListener() {}, querySelectorAll() { return []; },
        };
    }
    const sources = [];
    class EventSource {
        constructor(url) { this.url = url; this.listeners = {}; sources.push(this); }
        addEventListener(type, callback) { this.listeners[type] = callback; }
        emit(type, value) { this.listeners[type]?.({ data: JSON.stringify(value) }); }
        close() { this.closed = true; }
    }
    const context = vm.createContext({
        console, EventSource,
        document: {
            getElementById(id) { if (!elements.has(id)) elements.set(id, element()); return elements.get(id); },
            querySelectorAll() { return []; }, createElement: element,
        },
        fetch: async () => ({ json: async () => ({ sessions: [] }) }),
        setInterval() {},
        setTimeout() { throw new Error('Competing reconnect timer created'); },
    });
    const html = fs.readFileSync(path.join(__dirname, '../server/static/index.html'), 'utf8');
    vm.runInContext(html.match(/<script>([\s\S]*?)<\/script>/)[1], context);
    return { sources, run: code => vm.runInContext(code, context) };
}

test('native SSE reconnect preserves transcript and ignores duplicate lines', () => {
    const { sources, run } = environment();
    run('selectSession("one.jsonl")');
    const source = sources.at(-1);
    source.emit('reset', { path: 'one.jsonl' });
    source.emit('line', { line_num: 1, line: '{}' });
    source.onerror();
    assert.equal(run('rawLines.length'), 1);
    assert.equal(sources.length, 2); // one global, one selected session
    source.emit('line', { line_num: 1, line: '{}' });
    source.emit('line', { line_num: 2, line: '{}' });
    assert.equal(run('rawLines.length'), 2);
    source.emit('reset', { path: 'one.jsonl' });
    source.emit('line', { line_num: 1, line: '{"replacement":true}' });
    assert.equal(run('rawLines.length'), 1);
    assert.equal(run('rawLines[0].content'), '{"replacement":true}');
});

test('switching sessions ignores stale callbacks', () => {
    const { sources, run } = environment();
    run('selectSession("one.jsonl")');
    const old = sources.at(-1);
    run('selectSession("two.jsonl")');
    assert.equal(old.closed, true);
    sources.at(-1).emit('line', { line_num: 1, line: '{}' });
    old.emit('line', { line_num: 2, line: '{}' });
    old.emit('reset', {});
    old.onerror();
    assert.equal(run('rawLines.length'), 1);
    assert.equal(sources.length, 3);
});

test('global snapshots reconcile restarts and smaller histories without retry timers', () => {
    const { sources, run } = environment();
    const global = sources[0];
    global.emit('sessions', [{ path: 'one.jsonl', line_count: 10 }]);
    assert.equal(run('sessions[0].line_count'), 10);
    global.onerror();
    global.emit('sessions', [{ path: 'one.jsonl', line_count: 1 }]);
    assert.equal(run('sessions[0].line_count'), 1);
    global.emit('sessions', []);
    assert.equal(run('sessions.length'), 0);
    assert.equal(sources.length, 1);
});

test('Claude metadata, ordering and rendering survive snapshot-based streaming', () => {
    const { sources, run } = environment();
    const metadata = [
        { path: 'pi/same.jsonl', source: 'pi', updated_at: '2020-01-01T00:00:00Z', line_count: 1 },
        { path: 'claude/same.jsonl', source: 'claude', updated_at: '2020-01-02T00:00:00Z', line_count: 1, preview: 'Claude prompt' },
    ];
    sources[0].emit('sessions', metadata);
    assert.equal(run('sessions[0].source'), 'claude');
    assert.equal(run('sessions[0].preview'), 'Claude prompt');
    run('selectSession("claude/same.jsonl", "claude")');
    const selected = sources.at(-1);
    selected.emit('reset', {});
    selected.emit('line', { line_num: 1, source: 'claude', line: JSON.stringify({ type: 'user', message: { content: 'Claude prompt' } }) });
    assert.equal(run('prettyView.children[0].className'), 'msg msg-user');
    selected.onerror();
    sources[0].emit('sessions', metadata);
    assert.equal(run('sessions[0].preview'), 'Claude prompt');
    assert.equal(run('rawLines.length'), 1);
    run('selectSession("pi/same.jsonl", "pi")');
    sources.at(-1).emit('line', { line_num: 1, source: 'pi', line: JSON.stringify({ type: 'message', message: { role: 'user', content: 'Pi prompt' } }) });
    assert.equal(run('prettyView.children[0].className'), 'msg msg-user');
});
