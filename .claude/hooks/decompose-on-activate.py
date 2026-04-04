#!/usr/bin/env python3
"""
Conductor decompose-on-activate hook.

Intercepts mcp__conductor__set_status calls with status="active" and blocks
activation unless the task already has children (previously decomposed) or is
explicitly marked atomic via state.atomic=true.

This forces recursive decomposition: Claude must break a task into sub-tasks
before activating it, then activate the first child (which triggers this hook
again), repeating until leaf tasks are reached.

Atomic bypass: for tasks that are genuinely a single tool call, Claude should
call mcp__conductor__update_task with state_patch: {"atomic": true} first,
then retry set_status.

Fails open (exit 0) on any error so it never blocks legitimate work.
"""
import sys
import json
import os
import sqlite3

try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)

# Only intercept set_status calls
if data.get('tool_name') != 'mcp__conductor__set_status':
    sys.exit(0)

tool_input = data.get('tool_input', {})
if tool_input.get('status') != 'active':
    sys.exit(0)

task_id = tool_input.get('task_id', '')
if not task_id:
    sys.exit(0)

db_path = os.environ.get(
    'CONDUCTOR_DB',
    os.path.join(os.path.expanduser('~'), '.conductor', 'tasks.db')
)

try:
    conn = sqlite3.connect(f'file:{db_path}?mode=ro', uri=True)

    # Allow if task already has children (already decomposed)
    child_count = conn.execute(
        'SELECT COUNT(*) FROM tasks WHERE id LIKE ?',
        (f'{task_id}.%',)
    ).fetchone()[0]
    if child_count > 0:
        conn.close()
        sys.exit(0)

    # Allow if task is explicitly marked atomic in ANY plan (handles multiple plans
    # sharing the same short task_id like "1.3")
    rows = conn.execute(
        'SELECT state FROM tasks WHERE id = ?',
        (task_id,)
    ).fetchall()
    conn.close()
    for row in rows:
        if row and row[0]:
            try:
                state = json.loads(row[0])
                if state.get('atomic'):
                    sys.exit(0)
            except Exception:
                pass

    # Block and instruct Claude to decompose or mark atomic
    print(json.dumps({
        'hookSpecificOutput': {
            'hookEventName': 'PreToolUse',
            'permissionDecision': 'deny',
            'permissionDecisionReason': (
                'CONDUCTOR DECOMPOSE: This task has no sub-tasks yet. '
                'Read the task goal and choose one path:\n'
                '1. Multi-step work: call mcp__conductor__provision_tasks to create child tasks, '
                'then set the first child to active (leave this parent pending). '
                'The hook will fire again on each child, recursively decomposing until leaf tasks.\n'
                '2. Single atomic operation (one specific tool call): call '
                'mcp__conductor__update_task with state_patch: {"atomic": true}, '
                'then retry mcp__conductor__set_status.'
            )
        }
    }))

except Exception:
    sys.exit(0)  # Fail open on any DB or parse error
