(function() {
    'use strict';

    // ================================================================
    // Section 1: City Scope + WS Connection
    // ================================================================

    var _selectedCity = new URLSearchParams(window.location.search).get('city') || '';

    var _wsUrl = (window.location.protocol === 'https:' ? 'wss://' : 'ws://') +
        window.location.host + '/v0/ws';

    window.wsConnected = false;
    Object.defineProperty(window, 'sseConnected', {
        get: function() { return window.wsConnected; },
        set: function(v) { window.wsConnected = v; }
    });
    window.pauseRefresh = false;

    var _ws = null;
    var _wsReqId = 0;
    var _wsPending = {};
    var _wsReconnectDelay = 1000;
    var _wsMaxReconnectDelay = 30000;
    var _lastEventCursor = '';
    var _subscriptionRetry = 0;
    var _wsCapabilities = [];
    var _citiesList = [];

    // Category-based refresh: observation events update only the activity
    // panel (cheap). State-changing events trigger a full dashboard reload
    // but are debounced to prevent overload.
    var _observationTypes = {
        'agent.message': 1, 'agent.tool_call': 1, 'agent.tool_result': 1,
        'agent.thinking': 1, 'agent.output': 1, 'agent.idle': 1,
        'agent.error': 1, 'agent.completed': 1
    };

    var _activityTimer = null;
    var _activityThrottle = 2000;
    var _fullRefreshTimer = null;

    function wsRequest(action, payload) {
        return new Promise(function(resolve, reject) {
            if (!_ws || _ws.readyState !== WebSocket.OPEN) {
                reject(new Error('WebSocket not connected'));
                return;
            }
            _wsReqId++;
            var id = 'dash-' + _wsReqId;
            var msg = {type: 'request', id: id, action: action};
            if (_selectedCity) {
                msg.scope = {city: _selectedCity};
            }
            if (payload) {
                msg.payload = payload;
            }
            _wsPending[id] = {resolve: resolve, reject: reject};
            _ws.send(JSON.stringify(msg));
            setTimeout(function() {
                if (_wsPending[id]) {
                    _wsPending[id].reject(new Error('WebSocket request timeout'));
                    delete _wsPending[id];
                }
            }, 30000);
        });
    }

    function connectWebSocket() {
        if (_ws) {
            _ws.close();
        }

        _ws = new WebSocket(_wsUrl);

        _ws.onopen = function() {
            // Hello envelope arrives as the first message.
        };

        _ws.onmessage = function(e) {
            var msg;
            try { msg = JSON.parse(e.data); } catch (err) { return; }

            switch (msg.type) {
            case 'hello':
                window.wsConnected = true;
                _wsReconnectDelay = 1000;
                _subscriptionRetry = 0;
                updateConnectionStatus('live');
                _buildCommandsFromCapabilities(msg.capabilities);
                _resolveDefaultCity(msg).then(function() {
                    _subscribeEvents();
                    loadDashboard();
                });
                break;
            case 'response':
                if (msg.id && _wsPending[msg.id]) {
                    _wsPending[msg.id].resolve(msg.result);
                    delete _wsPending[msg.id];
                }
                break;
            case 'error':
                if (msg.id && _wsPending[msg.id]) {
                    _wsPending[msg.id].reject(new Error(msg.message || msg.code || 'API error'));
                    delete _wsPending[msg.id];
                }
                break;
            case 'event':
                _handleWSEvent(msg);
                break;
            }
        };

        _ws.onclose = function() {
            window.wsConnected = false;
            updateConnectionStatus('reconnecting');
            setTimeout(function() {
                _wsReconnectDelay = Math.min(_wsReconnectDelay * 2, _wsMaxReconnectDelay);
                connectWebSocket();
            }, _wsReconnectDelay);
        };

        _ws.onerror = function() {
            // onclose fires after onerror.
        };
    }

    function _resolveDefaultCity(hello) {
        if (_selectedCity) return Promise.resolve();
        if (!hello || hello.server_role !== 'supervisor') return Promise.resolve();
        return wsRequest('cities.list').then(function(data) {
            var items = (data && data.items) || [];
            _citiesList = items;
            if (items.length > 0 && items[0].name) {
                _selectedCity = items[0].name;
            }
            updateCityTabs(items);
        }).catch(function(err) {
            handleError(err, 'cities.list.default');
        });
    }

    function updateConnectionStatus(state) {
        var el = document.getElementById('connection-status');
        if (!el) return;
        switch (state) {
        case 'live':
            el.textContent = 'Live';
            el.className = 'connection-live';
            break;
        case 'reconnecting':
            el.textContent = 'Reconnecting...';
            el.className = 'connection-reconnecting';
            break;
        default:
            el.textContent = 'Connecting...';
            el.className = '';
        }
    }

    // ================================================================
    // Section 2: Helpers
    // ================================================================

    function escapeHtml(str) {
        if (str === null || str === undefined) return '';
        str = String(str);
        var div = document.createElement('div');
        div.textContent = str;
        return div.innerHTML;
    }

    function handleError(err, context) {
        console.warn('dashboard [' + context + ']:', err);
    }

    function on(id, event, handler) {
        var el = document.getElementById(id);
        if (el) el.addEventListener(event, handler);
    }

    function formatAge(isoDate) {
        if (!isoDate) return '';
        var d;
        if (typeof isoDate === 'string') {
            d = new Date(isoDate);
        } else if (isoDate instanceof Date) {
            d = isoDate;
        } else {
            return '';
        }
        if (isNaN(d.getTime())) return '';
        var now = Date.now();
        var diffMs = now - d.getTime();
        if (diffMs < 0) diffMs = 0;
        var diffSec = Math.floor(diffMs / 1000);
        if (diffSec < 60) return 'just now';
        var diffMin = Math.floor(diffSec / 60);
        if (diffMin < 60) return diffMin + 'm';
        var diffHour = Math.floor(diffMin / 60);
        if (diffHour < 24) return diffHour + 'h';
        var diffDay = Math.floor(diffHour / 24);
        return diffDay + 'd';
    }

    function formatTimestamp(isoDate) {
        if (!isoDate) return '';
        var d = new Date(isoDate);
        if (isNaN(d.getTime())) return '';
        var now = new Date();
        var months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
        var hours = d.getHours();
        var minutes = d.getMinutes();
        var ampm = hours >= 12 ? 'PM' : 'AM';
        hours = hours % 12 || 12;
        var minStr = minutes < 10 ? '0' + minutes : String(minutes);
        var yearPart = d.getFullYear() !== now.getFullYear() ? ' ' + d.getFullYear() + ',' : '';
        return months[d.getMonth()] + ' ' + d.getDate() + yearPart + ', ' + hours + ':' + minStr + ' ' + ampm;
    }

    function showToast(type, title, message) {
        var container = document.getElementById('toast-container');
        if (!container) return;
        var toast = document.createElement('div');
        toast.className = 'toast ' + type;
        var icon = type === 'success' ? '\u2713' : type === 'error' ? '\u2715' : '\u2139';
        toast.innerHTML = '<span class="toast-icon">' + icon + '</span>' +
            '<div class="toast-content">' +
            '<div class="toast-title">' + escapeHtml(title) + '</div>' +
            '<div class="toast-message">' + escapeHtml(message) + '</div>' +
            '</div>' +
            '<button class="toast-close">\u2715</button>';
        container.appendChild(toast);

        setTimeout(function() {
            if (toast.parentNode) toast.parentNode.removeChild(toast);
        }, 4000);

        var closeBtn = toast.querySelector('.toast-close');
        if (closeBtn) {
            closeBtn.onclick = function() {
                if (toast.parentNode) toast.parentNode.removeChild(toast);
            };
        }
    }

    // Compute work status from activity age and bead state.
    function computeWorkStatus(lastActivity, currentBeadId) {
        if (!currentBeadId) return 'idle';
        if (!lastActivity) return 'working';
        var d = new Date(lastActivity);
        if (isNaN(d.getTime())) return 'working';
        var ageMs = Date.now() - d.getTime();
        var ageMin = ageMs / 60000;
        if (ageMin < 5) return 'working';
        if (ageMin < 10) return 'stale';
        return 'stuck';
    }

    // Status badge CSS classes from status strings.
    function statusBadgeClass(status) {
        switch (status) {
        case 'working': case 'active': case 'running': case 'spinning':
            return 'badge-green';
        case 'stale':
            return 'badge-yellow';
        case 'stuck': case 'error': case 'critical':
            return 'badge-red';
        case 'idle': case 'ready': case 'detached':
            return 'badge-muted';
        case 'open':
            return 'badge-blue';
        case 'in_progress': case 'in progress':
            return 'badge-yellow';
        case 'closed': case 'complete': case 'done': case 'finished':
            return 'badge-green';
        default:
            return 'badge-muted';
        }
    }

    // Priority badge HTML.
    function priorityBadge(priority) {
        var p = parseInt(priority, 10);
        if (p === 1) return '<span class="badge badge-red">P1</span>';
        if (p === 2) return '<span class="badge badge-orange">P2</span>';
        if (p === 3) return '<span class="badge badge-yellow">P3</span>';
        return '<span class="badge badge-muted">P4</span>';
    }

    // Activity dot color class from age.
    function activityDotClass(isoDate) {
        if (!isoDate) return 'dot-unknown';
        var d = new Date(isoDate);
        if (isNaN(d.getTime())) return 'dot-unknown';
        var ageMin = (Date.now() - d.getTime()) / 60000;
        if (ageMin < 5) return 'dot-green';
        if (ageMin < 10) return 'dot-yellow';
        return 'dot-red';
    }

    // Event category from event type string.
    function eventCategory(type) {
        if (!type) return 'system';
        if (type.indexOf('agent.') === 0) return 'agent';
        if (type.indexOf('bead.') === 0 || type.indexOf('convoy.') === 0) return 'work';
        if (type.indexOf('mail.') === 0) return 'comms';
        return 'system';
    }

    // Event icon from event type string.
    function eventIcon(type) {
        if (!type) return '\uD83D\uDD14'; // bell
        var map = {
            'bead.created': '\uD83D\uDCFF',      // beads
            'bead.closed': '\u2705',               // check
            'bead.assigned': '\uD83D\uDC64',      // person
            'bead.reopened': '\uD83D\uDD04',       // arrows
            'mail.sent': '\u2709\uFE0F',            // envelope
            'mail.received': '\uD83D\uDCE8',       // incoming envelope
            'agent.started': '\uD83D\uDE80',       // rocket
            'agent.stopped': '\u23F9\uFE0F',        // stop
            'agent.error': '\u26A0\uFE0F',          // warning
            'convoy.created': '\uD83D\uDE9A',      // truck
            'convoy.closed': '\uD83C\uDFC1',       // flag
            'session.started': '\uD83D\uDD35',     // blue circle
            'session.ended': '\u26AB',              // black circle
            'sling.dispatched': '\uD83C\uDFAF'     // target
        };
        return map[type] || '\uD83D\uDD14';
    }

    // Event summary text.
    function eventSummary(type, actor, subject, message) {
        if (message) return message;
        var parts = (type || '').split('.');
        var action = parts.length > 1 ? parts[1] : parts[0];
        var entity = parts[0] || 'event';
        var who = subject || actor || 'system';
        return entity + ' ' + action + ' by ' + who;
    }

    // Extract rig from agent path like "myrig/polecats/polecat-1".
    function extractRig(agentPath) {
        if (!agentPath) return '';
        var parts = agentPath.split('/');
        if (parts.length >= 2) return parts[0];
        return '';
    }

    // Format agent address: "myrig/polecats/polecat-1" -> "polecat-1".
    function formatAgentAddress(addr) {
        if (!addr) return '';
        var parts = addr.split('/');
        return parts[parts.length - 1];
    }

    // Check if a bead is internal infrastructure.
    function isInternalBead(bead) {
        var internalTypes = ['message', 'convoy', 'queue', 'merge-request', 'wisp', 'agent'];
        if (internalTypes.indexOf(bead.type) !== -1) return true;
        var labels = bead.labels || [];
        for (var i = 0; i < labels.length; i++) {
            var l = labels[i];
            if (l === 'gc:message' || l === 'gc:convoy' || l === 'gc:queue' ||
                l === 'gc:merge-request' || l === 'gc:wisp' || l === 'gc:agent') return true;
        }
        return false;
    }

    // Group flat mail messages into threads by thread_id.
    function groupMailIntoThreads(messages) {
        var threads = {};
        (messages || []).forEach(function(msg) {
            var tid = msg.thread_id || msg.id;
            if (!threads[tid]) {
                threads[tid] = {
                    id: tid,
                    subject: msg.subject,
                    messages: [],
                    unread_count: 0,
                    latest_at: msg.created_at,
                    last_message: msg
                };
            }
            threads[tid].messages.push(msg);
            if (!msg.read) threads[tid].unread_count++;
            if (msg.created_at > threads[tid].latest_at) {
                threads[tid].latest_at = msg.created_at;
                threads[tid].last_message = msg;
            }
        });
        var result = Object.values(threads);
        result.sort(function(a, b) {
            return (b.latest_at || '').localeCompare(a.latest_at || '');
        });
        // Add count and thread_id for each thread.
        result.forEach(function(t) {
            t.count = t.messages.length;
            t.thread_id = t.id;
        });
        return result;
    }

    // ================================================================
    // Section 3: Panel Renderers
    // ================================================================

    // --- (a) Convoys Panel ---
    function renderConvoysPanel(convoys) {
        var tbody = document.getElementById('convoy-tbody');
        var empty = document.getElementById('convoy-empty');
        var countEl = document.querySelector('#convoy-panel .panel-header .count');
        var table = tbody ? tbody.closest('table') : null;

        if (!tbody) return;

        if (!convoys || convoys.length === 0) {
            if (table) table.style.display = 'none';
            if (empty) empty.style.display = 'block';
            if (countEl) countEl.textContent = '0';
            return;
        }

        if (table) table.style.display = 'table';
        if (empty) empty.style.display = 'none';
        if (countEl) countEl.textContent = String(convoys.length);

        var html = '';
        for (var i = 0; i < convoys.length; i++) {
            var c = convoys[i];
            var total = c.child_count || c.total || 0;
            var completed = c.completed || 0;
            var pct = total > 0 ? Math.round((completed / total) * 100) : 0;

            // Work status badge
            var ws = c.work_status || c.status || 'open';
            var wsBadgeClass = statusBadgeClass(ws);

            // Work breakdown chips
            var readyBeads = c.ready_beads || 0;
            var inProgress = c.in_progress || 0;
            var workChips = '';
            if (completed > 0) workChips += '<span class="chip chip-green">' + completed + ' done</span> ';
            if (inProgress > 0) workChips += '<span class="chip chip-yellow">' + inProgress + ' wip</span> ';
            if (readyBeads > 0) workChips += '<span class="chip chip-blue">' + readyBeads + ' ready</span> ';
            if (!workChips) workChips = '<span class="chip chip-muted">' + total + ' total</span>';

            // Activity
            var actAge = c.last_activity ? formatAge(c.last_activity) : 'idle';
            var dotClass = c.last_activity ? activityDotClass(c.last_activity) : 'dot-unknown';

            html += '<tr class="convoy-row" data-convoy-id="' + escapeHtml(c.id) + '">' +
                '<td><span class="badge ' + wsBadgeClass + '">' + escapeHtml(ws) + '</span></td>' +
                '<td><span class="convoy-id">' + escapeHtml(c.id) + '</span> ' + escapeHtml(c.title || '') + '</td>' +
                '<td><div class="progress-bar"><div class="progress-bar-fill" style="width:' + pct + '%"></div></div> <span class="progress-text">' + completed + '/' + total + '</span></td>' +
                '<td>' + workChips + '</td>' +
                '<td><span class="activity-dot ' + dotClass + '"></span> ' + escapeHtml(actAge) + '</td>' +
                '</tr>';
        }
        tbody.innerHTML = html;
    }

    // --- (b) Crew Panel ---
    function renderCrewPanel(sessions) {
        var tbody = document.getElementById('crew-tbody');
        var table = document.getElementById('crew-table');
        var loading = document.getElementById('crew-loading');
        var empty = document.getElementById('crew-empty');
        var countEl = document.getElementById('crew-count');

        if (!tbody) return;
        if (loading) loading.style.display = 'none';

        // Crew: named, long-lived pool members with a rig.
        var crew = (sessions || []).filter(function(s) {
            return s.rig && s.pool;
        });

        if (crew.length === 0) {
            if (table) table.style.display = 'none';
            if (empty) empty.style.display = 'block';
            if (countEl) countEl.textContent = '0';
            return;
        }

        if (table) table.style.display = 'table';
        if (empty) empty.style.display = 'none';
        if (countEl) countEl.textContent = String(crew.length);

        // Check for state changes.
        checkCrewNotifications(crew);

        var html = '';
        for (var i = 0; i < crew.length; i++) {
            var m = crew[i];
            var name = m.template || m.title || m.id || '';
            var stateText = m.state || 'unknown';
            var stateClass = statusBadgeClass(stateText);

            var beadInfo = m.current_bead_id
                ? '<span class="issue-id">' + escapeHtml(m.current_bead_id) + '</span>' +
                  (m.current_bead_title ? ' ' + escapeHtml(m.current_bead_title) : '')
                : '\u2014';

            var actAge = m.last_activity ? formatAge(m.last_activity) : '\u2014';

            var sessionBadge = '';
            if (m.attached) {
                sessionBadge = '<span class="badge badge-green">Attached</span>';
            } else {
                sessionBadge = '<span class="badge badge-muted">' + escapeHtml(m.session_name || m.id || '') + '</span>';
            }

            html += '<tr class="crew-' + escapeHtml(stateText) + '">' +
                '<td><a href="#" class="agent-log-link" data-agent-name="' + escapeHtml(name) + '">' + escapeHtml(name) + '</a></td>' +
                '<td><span class="crew-rig">' + escapeHtml(m.rig || '') + '</span></td>' +
                '<td><span class="badge ' + stateClass + '">' + escapeHtml(stateText) + '</span></td>' +
                '<td>' + beadInfo + '</td>' +
                '<td class="crew-activity">' + escapeHtml(actAge) + '</td>' +
                '<td>' + sessionBadge + '</td>' +
                '<td><button class="attach-btn" data-agent="' + escapeHtml(name) + '" title="Actions">\u22EF</button></td>' +
                '</tr>';
        }
        tbody.innerHTML = html;
    }

    // Crew notification system.
    var previousCrewStates = {};
    var crewNeedsAttention = 0;

    function checkCrewNotifications(crewList) {
        var newNeedsAttention = 0;
        crewList.forEach(function(member) {
            var name = member.template || member.title || member.id || '';
            var key = (member.rig || '') + '/' + name;
            var prevState = previousCrewStates[key];
            var newState = member.state;
            if (newState === 'finished' || newState === 'questions') {
                newNeedsAttention++;
            }
            if (prevState && prevState !== newState) {
                if (newState === 'finished') {
                    showToast('success', 'Crew Finished', name + ' finished their work!');
                    playNotificationSound();
                } else if (newState === 'questions') {
                    showToast('info', 'Needs Attention', name + ' has questions for you');
                    playNotificationSound();
                }
            }
            previousCrewStates[key] = newState;
        });
        crewNeedsAttention = newNeedsAttention;
        var countEl = document.getElementById('crew-count');
        if (countEl) {
            if (crewNeedsAttention > 0) {
                countEl.classList.add('needs-attention');
                countEl.setAttribute('data-attention', crewNeedsAttention);
            } else {
                countEl.classList.remove('needs-attention');
                countEl.removeAttribute('data-attention');
            }
        }
    }

    function playNotificationSound() {
        try {
            var ctx = new (window.AudioContext || window.webkitAudioContext)();
            var osc = ctx.createOscillator();
            var gain = ctx.createGain();
            osc.connect(gain);
            gain.connect(ctx.destination);
            osc.frequency.value = 800;
            gain.gain.value = 0.1;
            osc.start();
            osc.stop(ctx.currentTime + 0.1);
        } catch (e) { /* audio not available */ }
    }

    // --- (c) Polecats Panel ---
    function renderPolecatsPanel(sessions) {
        var tbody = document.getElementById('polecats-tbody');
        var table = document.getElementById('polecats-table');
        var empty = document.getElementById('polecats-empty');
        var countEl = document.querySelector('#polecats-panel .panel-header .count');

        if (!tbody) return;

        // Polecats: pool workers (have pool set, have rig set).
        var polecats = (sessions || []).filter(function(s) {
            return s.pool && s.rig;
        });

        if (polecats.length === 0) {
            if (table) table.style.display = 'none';
            if (empty) empty.style.display = 'block';
            if (countEl) countEl.textContent = '0';
            return;
        }

        if (table) table.style.display = 'table';
        if (empty) empty.style.display = 'none';
        if (countEl) countEl.textContent = String(polecats.length);

        var html = '';
        for (var i = 0; i < polecats.length; i++) {
            var p = polecats[i];
            var name = p.template || p.title || p.id || '';
            var poolType = p.pool || 'polecat';
            var typeBadgeClass = poolType === 'refinery' ? 'badge-purple' : 'badge-blue';

            var workingOn = '\u2014';
            if (p.current_bead_id) {
                workingOn = '<span class="issue-id">' + escapeHtml(p.current_bead_id) + '</span>';
                if (p.current_bead_title) {
                    workingOn += ' ' + escapeHtml(p.current_bead_title);
                }
            }

            var ws = computeWorkStatus(p.last_activity, p.current_bead_id);
            var wsBadgeClass = statusBadgeClass(ws);

            var actAge = p.last_activity ? formatAge(p.last_activity) : '\u2014';
            var dotClass = p.last_activity ? activityDotClass(p.last_activity) : 'dot-unknown';

            html += '<tr>' +
                '<td><a href="#" class="agent-log-link" data-agent-name="' + escapeHtml(name) + '">' + escapeHtml(name) + '</a></td>' +
                '<td><span class="badge ' + typeBadgeClass + '">' + escapeHtml(poolType) + '</span></td>' +
                '<td><span class="crew-rig">' + escapeHtml(p.rig || '') + '</span></td>' +
                '<td>' + workingOn + '</td>' +
                '<td><span class="badge ' + wsBadgeClass + '">' + escapeHtml(ws) + '</span></td>' +
                '<td><span class="activity-dot ' + dotClass + '"></span> ' + escapeHtml(actAge) + '</td>' +
                '</tr>';
        }
        tbody.innerHTML = html;
    }

    // --- (d) Activity Panel ---
    function renderActivityPanel(events) {
        var timeline = document.getElementById('activity-timeline');
        var emptyState = document.getElementById('activity-empty');
        var filters = document.getElementById('activity-filters');
        var countEl = document.querySelector('#activity-panel .panel-header .count');

        if (!timeline) return;

        if (!events || events.length === 0) {
            timeline.innerHTML = '';
            if (emptyState) emptyState.style.display = 'block';
            if (filters) filters.style.display = 'none';
            if (countEl) countEl.textContent = '0';
            return;
        }

        if (emptyState) emptyState.style.display = 'none';
        if (filters) filters.style.display = 'flex';
        if (countEl) countEl.textContent = String(events.length);

        // Populate rig and agent filter dropdowns.
        var rigSet = {};
        var agentSet = {};
        for (var k = 0; k < events.length; k++) {
            var ev = events[k];
            var eRig = ev.rig || extractRig(ev.actor || ev.subject || '');
            if (eRig) rigSet[eRig] = true;
            var eAgent = ev.actor || ev.subject || '';
            if (eAgent) agentSet[formatAgentAddress(eAgent)] = true;
        }

        var rigFilter = document.getElementById('tl-rig-filter');
        if (rigFilter) {
            var rigVal = rigFilter.value;
            rigFilter.innerHTML = '<option value="all">All rigs</option>';
            Object.keys(rigSet).sort().forEach(function(r) {
                rigFilter.innerHTML += '<option value="' + escapeHtml(r) + '">' + escapeHtml(r) + '</option>';
            });
            rigFilter.value = rigVal;
        }

        var agentFilter = document.getElementById('tl-agent-filter');
        if (agentFilter) {
            var agentVal = agentFilter.value;
            agentFilter.innerHTML = '<option value="all">All agents</option>';
            Object.keys(agentSet).sort().forEach(function(a) {
                agentFilter.innerHTML += '<option value="' + escapeHtml(a) + '">' + escapeHtml(a) + '</option>';
            });
            agentFilter.value = agentVal;
        }

        var html = '';
        for (var i = 0; i < events.length; i++) {
            var e = events[i];
            var cat = eventCategory(e.type);
            var icon = eventIcon(e.type);
            var summary = eventSummary(e.type, e.actor, e.subject, e.message);
            var time = e.created_at ? formatAge(e.created_at) : (e.ts ? formatAge(e.ts) : '');
            var rig = e.rig || extractRig(e.actor || e.subject || '');
            var agent = formatAgentAddress(e.actor || e.subject || '');

            html += '<div class="tl-entry tl-' + cat + '"' +
                ' data-category="' + cat + '"' +
                ' data-rig="' + escapeHtml(rig) + '"' +
                ' data-agent="' + escapeHtml(agent) + '">' +
                '<span class="tl-time">' + escapeHtml(time) + '</span>' +
                '<span class="tl-icon">' + icon + '</span>' +
                '<span class="tl-summary">' + escapeHtml(summary) + '</span>';

            if (agent) html += ' <span class="badge badge-muted tl-badge">' + escapeHtml(agent) + '</span>';
            if (rig) html += ' <span class="badge badge-blue tl-badge">' + escapeHtml(rig) + '</span>';
            html += ' <span class="badge badge-muted tl-badge">' + escapeHtml(e.type || '') + '</span>';
            html += '</div>';
        }
        timeline.innerHTML = html;

        // Apply current filters.
        applyActivityFilters();
    }

    // Activity filter state.
    var _activityCategoryFilter = 'all';
    var _activityRigFilter = 'all';
    var _activityAgentFilter = 'all';

    function applyActivityFilters() {
        var entries = document.querySelectorAll('#activity-timeline .tl-entry');
        var visibleCount = 0;
        for (var i = 0; i < entries.length; i++) {
            var entry = entries[i];
            var cat = entry.getAttribute('data-category') || '';
            var rig = entry.getAttribute('data-rig') || '';
            var agent = entry.getAttribute('data-agent') || '';
            var catMatch = _activityCategoryFilter === 'all' || cat === _activityCategoryFilter;
            var rigMatch = _activityRigFilter === 'all' || rig === _activityRigFilter;
            var agentMatch = _activityAgentFilter === 'all' || agent === _activityAgentFilter;
            if (catMatch && rigMatch && agentMatch) {
                entry.style.display = '';
                visibleCount++;
            } else {
                entry.style.display = 'none';
            }
        }
        var emptyFiltered = document.getElementById('tl-empty-filtered');
        if (emptyFiltered) {
            emptyFiltered.style.display = (visibleCount === 0 && entries.length > 0) ? 'block' : 'none';
        }
    }

    // --- (e) Mail All Panel ---
    function renderMailAllPanel(messages) {
        var tbody = document.getElementById('mail-all-tbody');
        var empty = document.getElementById('mail-all-empty');

        if (!tbody) return;

        if (!messages || messages.length === 0) {
            tbody.innerHTML = '';
            if (empty) empty.style.display = 'block';
            return;
        }
        if (empty) empty.style.display = 'none';

        // Sort by created_at descending.
        var sorted = messages.slice().sort(function(a, b) {
            return (b.created_at || '').localeCompare(a.created_at || '');
        });

        var html = '';
        for (var i = 0; i < sorted.length; i++) {
            var m = sorted[i];
            var priorityIcon = '';
            var pri = m.priority;
            if (pri === 0 || pri === 'urgent') priorityIcon = '<span class="priority-urgent">\u26A1</span> ';
            else if (pri === 1 || pri === 'high') priorityIcon = '<span class="priority-high">!</span> ';

            html += '<tr class="mail-row" data-msg-id="' + escapeHtml(m.id) + '" data-from="' + escapeHtml(m.from || '') + '">' +
                '<td>' + escapeHtml(formatAgentAddress(m.from)) + '</td>' +
                '<td>' + escapeHtml(formatAgentAddress(m.to)) + '</td>' +
                '<td>' + priorityIcon + escapeHtml(m.subject || '') + '</td>' +
                '<td>' + escapeHtml(m.created_at ? formatAge(m.created_at) : '') + '</td>' +
                '</tr>';
        }
        tbody.innerHTML = html;
    }

    // --- (f) Escalations Panel ---
    function renderEscalationsPanel(escalations) {
        var tbody = document.getElementById('escalations-tbody');
        var table = document.getElementById('escalations-table');
        var empty = document.getElementById('escalations-empty');
        var countEl = document.querySelector('#escalations-panel .panel-header .count');

        if (!tbody) return;

        if (!escalations || escalations.length === 0) {
            if (table) table.style.display = 'none';
            if (empty) empty.style.display = 'block';
            if (countEl) countEl.textContent = '0';
            return;
        }

        if (table) table.style.display = 'table';
        if (empty) empty.style.display = 'none';
        if (countEl) countEl.textContent = String(escalations.length);

        var html = '';
        for (var i = 0; i < escalations.length; i++) {
            var e = escalations[i];
            var severity = e.severity || 'medium';
            var sevClass = 'badge-muted';
            if (severity === 'critical') sevClass = 'badge-red';
            else if (severity === 'high') sevClass = 'badge-orange';
            else if (severity === 'medium') sevClass = 'badge-yellow';
            else if (severity === 'low') sevClass = 'badge-muted';

            var ackBadge = e.acked ? ' <span class="badge badge-green">acked</span>' : '';
            var escalatedBy = e.escalated_by || formatAgentAddress(e.from || '');
            var age = e.created_at ? formatAge(e.created_at) : (e.age || '');

            var actions = '';
            if (!e.acked) {
                actions += '<button class="btn-small escalation-action-btn" data-action="ack" data-id="' + escapeHtml(e.id) + '">Ack</button> ';
            }
            actions += '<button class="btn-small escalation-action-btn" data-action="resolve" data-id="' + escapeHtml(e.id) + '">Resolve</button> ';
            actions += '<button class="btn-small escalation-action-btn" data-action="reassign" data-id="' + escapeHtml(e.id) + '">Reassign</button>';

            html += '<tr>' +
                '<td><span class="badge ' + sevClass + '">' + escapeHtml(severity) + '</span></td>' +
                '<td>' + escapeHtml(e.title || e.id || '') + ackBadge + '</td>' +
                '<td>' + escapeHtml(escalatedBy) + '</td>' +
                '<td>' + escapeHtml(age) + '</td>' +
                '<td>' + actions + '</td>' +
                '</tr>';
        }
        tbody.innerHTML = html;
    }

    // --- (g) Services Panel ---
    function renderServicesPanel(services) {
        var tbody = document.getElementById('services-tbody');
        var table = document.getElementById('services-table');
        var empty = document.getElementById('services-empty');
        var countEl = document.querySelector('#services-panel .panel-header .count');

        if (!tbody) return;

        if (!services || services.length === 0) {
            if (table) table.style.display = 'none';
            if (empty) empty.style.display = 'block';
            if (countEl) countEl.textContent = '0';
            return;
        }

        if (table) table.style.display = 'table';
        if (empty) empty.style.display = 'none';
        if (countEl) countEl.textContent = String(services.length);

        var html = '';
        for (var i = 0; i < services.length; i++) {
            var svc = services[i];
            var name = svc.service_name || svc.name || '';
            html += '<tr>' +
                '<td><strong>' + escapeHtml(name) + '</strong></td>' +
                '<td>' + escapeHtml(svc.kind || '') + '</td>' +
                '<td><span class="badge ' + statusBadgeClass(svc.state || '') + '">' + escapeHtml(svc.state || '') + '</span></td>' +
                '<td><span class="badge ' + statusBadgeClass(svc.local_state || '') + '">' + escapeHtml(svc.local_state || '') + '</span></td>' +
                '</tr>';
        }
        tbody.innerHTML = html;
    }

    // --- (h) Rigs Panel ---
    function renderRigsPanel(rigs, sessions, agents) {
        var tbody = document.getElementById('rigs-tbody');
        var table = document.getElementById('rigs-table');
        var empty = document.getElementById('rigs-empty');
        var countEl = document.querySelector('#rigs-panel .panel-header .count');

        if (!tbody) return;

        if (!rigs || rigs.length === 0) {
            if (table) table.style.display = 'none';
            if (empty) empty.style.display = 'block';
            if (countEl) countEl.textContent = '0';
            return;
        }

        if (table) table.style.display = 'table';
        if (empty) empty.style.display = 'none';
        if (countEl) countEl.textContent = String(rigs.length);

        // Count polecats and crew per rig from sessions.
        var rigPolecats = {};
        var rigCrew = {};
        (sessions || []).forEach(function(s) {
            if (!s.rig) return;
            if (s.pool) {
                rigPolecats[s.rig] = (rigPolecats[s.rig] || 0) + 1;
            } else {
                rigCrew[s.rig] = (rigCrew[s.rig] || 0) + 1;
            }
        });

        // Count agents per rig.
        var rigAgentInfo = {};
        (agents || []).forEach(function(a) {
            if (!a.rig) return;
            if (!rigAgentInfo[a.rig]) rigAgentInfo[a.rig] = [];
            rigAgentInfo[a.rig].push(a);
        });

        var html = '';
        for (var i = 0; i < rigs.length; i++) {
            var r = rigs[i];
            var name = r.name || '';
            var pc = rigPolecats[name] || 0;
            var cc = rigCrew[name] || 0;
            var agentList = rigAgentInfo[name] || [];
            var agentIcons = '';
            for (var j = 0; j < agentList.length; j++) {
                var a = agentList[j];
                var tpl = a.template || a.name || '';
                if (tpl.indexOf('witness') !== -1) {
                    agentIcons += '<span class="badge badge-purple" title="' + escapeHtml(tpl) + '">\uD83D\uDC41</span> ';
                } else if (tpl.indexOf('refinery') !== -1) {
                    agentIcons += '<span class="badge badge-orange" title="' + escapeHtml(tpl) + '">\uD83C\uDFED</span> ';
                } else {
                    agentIcons += '<span class="badge badge-muted" title="' + escapeHtml(tpl) + '">\uD83E\uDD16</span> ';
                }
            }
            if (!agentIcons) agentIcons = '\u2014';

            html += '<tr>' +
                '<td><strong>' + escapeHtml(name) + '</strong></td>' +
                '<td>' + pc + '</td>' +
                '<td>' + cc + '</td>' +
                '<td>' + agentIcons + '</td>' +
                '</tr>';
        }
        tbody.innerHTML = html;
    }

    // --- (i) Dogs Panel ---
    function renderDogsPanel(sessions) {
        var tbody = document.getElementById('dogs-tbody');
        var table = document.getElementById('dogs-table');
        var empty = document.getElementById('dogs-empty');
        var countEl = document.querySelector('#dogs-panel .panel-header .count');

        if (!tbody) return;

        // Dogs: pool sessions with no rig.
        var dogs = (sessions || []).filter(function(s) {
            return s.pool && !s.rig;
        });

        if (dogs.length === 0) {
            if (table) table.style.display = 'none';
            if (empty) empty.style.display = 'block';
            if (countEl) countEl.textContent = '0';
            return;
        }

        if (table) table.style.display = 'table';
        if (empty) empty.style.display = 'none';
        if (countEl) countEl.textContent = String(dogs.length);

        var html = '';
        for (var i = 0; i < dogs.length; i++) {
            var d = dogs[i];
            var name = d.template || d.title || d.id || '';
            var state = d.current_bead_id ? 'working' : 'idle';
            var stateClass = statusBadgeClass(state);
            var workHint = d.status_hint || d.current_bead_title || '\u2014';
            var actAge = d.last_activity ? formatAge(d.last_activity) : '\u2014';

            html += '<tr>' +
                '<td>' + escapeHtml(name) + '</td>' +
                '<td><span class="badge ' + stateClass + '">' + escapeHtml(state) + '</span></td>' +
                '<td>' + escapeHtml(workHint) + '</td>' +
                '<td>' + escapeHtml(actAge) + '</td>' +
                '</tr>';
        }
        tbody.innerHTML = html;
    }

    // --- (j) Queues Panel ---
    function renderQueuesPanel(beads) {
        var queuePanel = document.getElementById('queues-panel');
        var tbody = document.getElementById('queues-tbody');
        var table = document.getElementById('queues-table');
        var empty = document.getElementById('queues-empty');
        var countEl = document.querySelector('#queues-panel .panel-header .count');

        if (!tbody || !queuePanel) return;

        // Filter beads with label gc:queue.
        var queues = (beads || []).filter(function(b) {
            var labels = b.labels || [];
            return labels.indexOf('gc:queue') !== -1;
        });

        if (queues.length === 0) {
            queuePanel.style.display = 'none';
            return;
        }

        queuePanel.style.display = '';
        if (table) table.style.display = 'table';
        if (empty) empty.style.display = 'none';
        if (countEl) countEl.textContent = String(queues.length);

        var html = '';
        for (var i = 0; i < queues.length; i++) {
            var q = queues[i];
            var name = q.title || q.id || '';
            var status = q.status || 'unknown';
            var sClass = statusBadgeClass(status);

            // Parse counts from description.
            var avail = 0, proc = 0, done = 0, fail = 0;
            var desc = q.description || '';
            var lines = desc.split('\n');
            for (var j = 0; j < lines.length; j++) {
                var line = lines[j].trim();
                if (line.indexOf('available_count:') === 0) avail = parseInt(line.split(':')[1], 10) || 0;
                else if (line.indexOf('processing_count:') === 0) proc = parseInt(line.split(':')[1], 10) || 0;
                else if (line.indexOf('completed_count:') === 0) done = parseInt(line.split(':')[1], 10) || 0;
                else if (line.indexOf('failed_count:') === 0) fail = parseInt(line.split(':')[1], 10) || 0;
            }

            html += '<tr>' +
                '<td>' + escapeHtml(name) + '</td>' +
                '<td><span class="badge ' + sClass + '">' + escapeHtml(status) + '</span></td>' +
                '<td>' + avail + '</td>' +
                '<td>' + proc + '</td>' +
                '<td>' + done + '</td>' +
                '<td>' + fail + '</td>' +
                '</tr>';
        }
        tbody.innerHTML = html;
    }

    // --- (k) Beads Panel ---
    function renderBeadsPanel(beads, rigs) {
        var tbody = document.getElementById('issues-tbody');
        var table = document.getElementById('work-table');
        var empty = document.getElementById('issues-empty');
        var countEl = document.querySelector('#beads-panel .panel-header .count');
        var rigFilterTabs = document.getElementById('rig-filter-tabs');

        if (!tbody) return;

        // Filter out internal beads.
        var filtered = (beads || []).filter(function(b) {
            return !isInternalBead(b);
        });

        // Sort by priority then age.
        filtered.sort(function(a, b) {
            var pa = a.priority || 5;
            var pb = b.priority || 5;
            if (pa !== pb) return pa - pb;
            return (b.created_at || '').localeCompare(a.created_at || '');
        });

        if (filtered.length === 0) {
            if (table) table.style.display = 'none';
            if (empty) empty.style.display = 'block';
            if (countEl) countEl.textContent = '0';
            return;
        }

        if (table) table.style.display = 'table';
        if (empty) empty.style.display = 'none';

        // Build rig filter buttons dynamically.
        var rigNames = {};
        filtered.forEach(function(b) {
            if (b.rig) rigNames[b.rig] = true;
        });
        // Also add rigs from rigs list.
        (rigs || []).forEach(function(r) {
            if (r.name) rigNames[r.name] = true;
        });

        if (rigFilterTabs) {
            var rigHtml = '<button class="rig-btn' + (currentRigFilter === 'all' ? ' active' : '') + '" data-rig="all" onclick="switchRigFilter(\'all\')">All</button>';
            Object.keys(rigNames).sort().forEach(function(rn) {
                rigHtml += '<button class="rig-btn' + (currentRigFilter === rn ? ' active' : '') + '" data-rig="' + escapeHtml(rn) + '" onclick="switchRigFilter(\'' + escapeHtml(rn) + '\')">' + escapeHtml(rn) + '</button>';
            });
            rigFilterTabs.innerHTML = rigHtml;
        }

        var html = '';
        for (var i = 0; i < filtered.length; i++) {
            var b = filtered[i];
            var status = b.status || 'open';
            var dataStatus = status === 'in_progress' ? 'progress' : (status === 'open' ? 'ready' : status);

            var statusCell = '';
            if (b.assignee) {
                statusCell = '<span class="badge badge-blue">' + escapeHtml(formatAgentAddress(b.assignee)) + '</span>';
            } else {
                statusCell = '<span class="badge ' + statusBadgeClass(status) + '">Ready</span>';
            }

            var age = b.created_at ? formatAge(b.created_at) : '';

            html += '<tr class="issue-row" data-bead-id="' + escapeHtml(b.id) + '"' +
                ' data-status="' + escapeHtml(dataStatus) + '"' +
                ' data-rig="' + escapeHtml(b.rig || '') + '">' +
                '<td>' + priorityBadge(b.priority) + '</td>' +
                '<td><span class="issue-id">' + escapeHtml(b.id) + '</span></td>' +
                '<td class="issue-title">' + escapeHtml(b.title || '') + '</td>' +
                '<td>' + escapeHtml(b.rig || '') + '</td>' +
                '<td>' + statusCell + '</td>' +
                '<td>' + escapeHtml(age) + '</td>' +
                '<td><button class="sling-btn" data-bead-id="' + escapeHtml(b.id) + '" title="Sling to rig">Sling</button></td>' +
                '</tr>';
        }
        tbody.innerHTML = html;

        // Apply current tab + rig filter.
        applyBeadsFilter();
    }

    // --- (l) Assigned Panel ---
    function renderAssignedPanel(assigned) {
        var tbody = document.getElementById('assigned-tbody');
        var table = document.getElementById('assigned-table');
        var empty = document.getElementById('assigned-empty');
        var countEl = document.querySelector('#assigned-panel .panel-header .count');
        var clearAllBtn = document.getElementById('assign-clear-all-btn');

        if (!tbody) return;

        if (!assigned || assigned.length === 0) {
            if (table) table.style.display = 'none';
            if (empty) empty.style.display = 'block';
            if (countEl) countEl.textContent = '0';
            if (clearAllBtn) clearAllBtn.style.display = 'none';
            return;
        }

        if (table) table.style.display = 'table';
        if (empty) empty.style.display = 'none';
        if (countEl) countEl.textContent = String(assigned.length);
        if (clearAllBtn) clearAllBtn.style.display = 'inline-block';

        // Sort: stale first, then by age.
        assigned.sort(function(a, b) {
            var aStale = a.created_at && (Date.now() - new Date(a.created_at).getTime()) > 3600000;
            var bStale = b.created_at && (Date.now() - new Date(b.created_at).getTime()) > 3600000;
            if (aStale !== bStale) return aStale ? -1 : 1;
            return (b.created_at || '').localeCompare(a.created_at || '');
        });

        var html = '';
        for (var i = 0; i < assigned.length; i++) {
            var a = assigned[i];
            var age = a.created_at ? formatAge(a.created_at) : '';
            var isStale = a.created_at && (Date.now() - new Date(a.created_at).getTime()) > 3600000;
            var staleBadge = isStale ? ' <span class="badge badge-yellow">stale</span>' : '';

            html += '<tr>' +
                '<td><span class="issue-id">' + escapeHtml(a.id) + '</span></td>' +
                '<td>' + escapeHtml(a.title || '') + '</td>' +
                '<td>' + escapeHtml(formatAgentAddress(a.assignee)) + '</td>' +
                '<td>' + escapeHtml(age) + staleBadge + '</td>' +
                '<td><button class="unassign-btn btn-small" data-bead-id="' + escapeHtml(a.id) + '">Unassign</button></td>' +
                '</tr>';
        }
        tbody.innerHTML = html;
    }

    // --- (m) Mayor Banner ---
    function updateMayorBanner(sessions) {
        var banner = document.getElementById('mayor-banner');
        if (!banner) return;

        // Mayor: city-scoped, non-pool session.
        var mayor = null;
        (sessions || []).forEach(function(s) {
            if (!s.pool && !s.rig) {
                mayor = s;
            }
        });

        if (!mayor) {
            banner.className = 'mayor-banner detached';
            var badgeEl = banner.querySelector('.mayor-info .badge');
            if (badgeEl) {
                badgeEl.textContent = 'No Mayor';
                badgeEl.className = 'badge badge-muted';
            }
            var statusDiv = banner.querySelector('.mayor-status');
            if (statusDiv) statusDiv.style.display = 'none';
            return;
        }

        var isAttached = mayor.attached;
        banner.className = 'mayor-banner ' + (isAttached ? 'attached' : 'detached');

        var badgeEl2 = banner.querySelector('.mayor-info .badge');
        if (badgeEl2) {
            badgeEl2.textContent = isAttached ? 'Attached' : 'Detached';
            badgeEl2.className = 'badge ' + (isAttached ? 'badge-green' : 'badge-muted');
        }

        var statusDiv2 = banner.querySelector('.mayor-status');
        if (statusDiv2) {
            statusDiv2.style.display = '';
            var statValues = statusDiv2.querySelectorAll('.mayor-stat-value');
            if (statValues.length >= 2) {
                var actAge = mayor.last_activity ? formatAge(mayor.last_activity) : 'unknown';
                statValues[0].textContent = actAge;
                var runtime = mayor.created_at ? formatAge(mayor.created_at) : 'unknown';
                statValues[1].textContent = runtime;
            }
        }
    }

    // --- (n) Summary Banner ---
    function updateSummaryBanner(data) {
        var banner = document.getElementById('summary-banner');
        if (!banner) return;

        var sessions = data.sessions || [];
        var beadsOpen = data.beadsOpen || [];
        var beadsInProgress = data.beadsInProgress || [];
        var convoys = data.convoys || [];
        var escalations = data.escalations || [];

        // Compute counts.
        var polecats = sessions.filter(function(s) { return s.pool && s.rig; });
        var assigned = beadsInProgress.filter(function(b) { return b.assignee; });

        var polecatCount = polecats.length;
        var assignedCount = assigned.length;
        var issueCount = beadsOpen.length;
        var convoyCount = convoys.length;
        var escalationCount = escalations.length;

        // Update stat values.
        var pcEl = document.getElementById('summary-polecat-count');
        var acEl = document.getElementById('summary-assigned-count');
        var icEl = document.getElementById('summary-issue-count');
        var ccEl = document.getElementById('summary-convoy-count');
        var ecEl = document.getElementById('summary-escalation-count');
        if (pcEl) pcEl.textContent = polecatCount;
        if (acEl) acEl.textContent = assignedCount;
        if (icEl) icEl.textContent = issueCount;
        if (ccEl) ccEl.textContent = convoyCount;
        if (ecEl) ecEl.textContent = escalationCount;

        // Compute alerts.
        var alerts = [];
        var stuckPolecats = polecats.filter(function(s) {
            return computeWorkStatus(s.last_activity, s.current_bead_id) === 'stuck';
        });
        if (stuckPolecats.length > 0) {
            alerts.push({type: 'red', text: stuckPolecats.length + ' stuck polecat(s)'});
        }
        var staleAssigned = assigned.filter(function(b) {
            return b.created_at && (Date.now() - new Date(b.created_at).getTime()) > 3600000;
        });
        if (staleAssigned.length > 0) {
            alerts.push({type: 'yellow', text: staleAssigned.length + ' stale assignment(s)'});
        }
        var unackedEscalations = escalations.filter(function(e) { return !e.acked; });
        if (unackedEscalations.length > 0) {
            alerts.push({type: 'red', text: unackedEscalations.length + ' unacked escalation(s)'});
        }
        var highPriBeads = beadsOpen.filter(function(b) { return b.priority === 1; });
        if (highPriBeads.length > 0) {
            alerts.push({type: 'orange', text: highPriBeads.length + ' P1 issue(s)'});
        }
        var deadSessions = sessions.filter(function(s) {
            return s.state === 'dead' || s.state === 'crashed' || s.state === 'error';
        });
        if (deadSessions.length > 0) {
            alerts.push({type: 'red', text: deadSessions.length + ' dead session(s)'});
        }

        // Show/hide banner.
        var anyData = polecatCount > 0 || assignedCount > 0 || issueCount > 0 || convoyCount > 0 || escalationCount > 0;
        banner.style.display = anyData ? '' : 'none';

        // Render alerts.
        var alertsDiv = document.getElementById('summary-alerts');
        if (alertsDiv) {
            if (alerts.length === 0) {
                alertsDiv.innerHTML = '<span class="alert-item alert-green">\u2713 All clear</span>';
            } else {
                var alertHtml = '';
                for (var i = 0; i < alerts.length; i++) {
                    alertHtml += '<span class="alert-item alert-' + alerts[i].type + '">' + escapeHtml(alerts[i].text) + '</span> ';
                }
                alertsDiv.innerHTML = alertHtml;
            }
        }
    }

    // --- (o) City Tabs ---
    function updateCityTabs(cities) {
        var nav = document.getElementById('city-tabs-nav');
        if (!nav) return;

        if (!cities || cities.length === 0) {
            nav.style.display = 'none';
            return;
        }

        nav.style.display = '';
        var html = '';
        for (var i = 0; i < cities.length; i++) {
            var c = cities[i];
            var isActive = c.name === _selectedCity;
            var runningDot = c.running ? '<span class="city-tab-dot dot-green"></span>' : '<span class="city-tab-dot dot-red"></span>';
            html += '<a href="?city=' + encodeURIComponent(c.name) + '" class="city-tab' + (isActive ? ' active' : '') + '">' +
                runningDot + ' ' + escapeHtml(c.name) + '</a>';
        }
        nav.innerHTML = html;
    }

    // ================================================================
    // Section 4: Data Loading
    // ================================================================

    function loadDashboard() {
        Promise.all([
            wsRequest('convoys.list').catch(function(err) { handleError(err, 'loadDashboard.convoys'); return {items: []}; }),
            wsRequest('sessions.list', {state: 'active', peek: true}).catch(function(err) { handleError(err, 'loadDashboard.sessions'); return {items: []}; }),
            wsRequest('mail.list').catch(function(err) { handleError(err, 'loadDashboard.mail'); return {items: []}; }),
            wsRequest('beads.list', {status: 'open'}).catch(function(err) { handleError(err, 'loadDashboard.beadsOpen'); return {items: []}; }),
            wsRequest('beads.list', {status: 'in_progress'}).catch(function(err) { handleError(err, 'loadDashboard.beadsIP'); return {items: []}; }),
            wsRequest('events.list', {limit: 50}).catch(function(err) { handleError(err, 'loadDashboard.events'); return {items: []}; }),
            wsRequest('status.get').catch(function(err) { handleError(err, 'loadDashboard.status'); return {}; }),
            wsRequest('rigs.list').catch(function(err) { handleError(err, 'loadDashboard.rigs'); return {items: []}; }),
            wsRequest('services.list').catch(function(err) { handleError(err, 'loadDashboard.services'); return {items: []}; }),
            wsRequest('agents.list').catch(function(err) { handleError(err, 'loadDashboard.agents'); return {items: []}; })
        ]).then(function(results) {
            var convoys = results[0].items || [];
            var sessions = results[1].items || [];
            var mail = results[2].items || [];
            var beadsOpen = results[3].items || [];
            var beadsInProgress = results[4].items || [];
            var events = results[5].items || [];
            // results[6] = status.get
            var rigs = results[7].items || [];
            var services = results[8].items || [];
            var agents = results[9].items || [];

            // Merge open + in_progress for the beads panel.
            var allBeads = beadsOpen.concat(beadsInProgress);

            // Compute escalations from beads labeled gc:escalation.
            var escalations = beadsOpen.filter(function(b) {
                var labels = b.labels || [];
                return labels.indexOf('gc:escalation') !== -1;
            }).map(function(b) {
                var severity = 'medium';
                var acked = false;
                (b.labels || []).forEach(function(l) {
                    if (l.indexOf('severity:') === 0) severity = l.replace('severity:', '');
                    if (l === 'acked') acked = true;
                });
                return {
                    id: b.id,
                    title: b.title,
                    severity: severity,
                    acked: acked,
                    escalated_by: formatAgentAddress(b.from || ''),
                    created_at: b.created_at,
                    from: b.from
                };
            });
            // Sort escalations by severity.
            var sevOrder = {critical: 0, high: 1, medium: 2, low: 3};
            escalations.sort(function(a, b) {
                return (sevOrder[a.severity] || 3) - (sevOrder[b.severity] || 3);
            });

            // Compute assigned from in_progress beads.
            var assigned = beadsInProgress.filter(function(b) { return b.assignee; });

            // Render all panels.
            renderConvoysPanel(convoys);
            renderCrewPanel(sessions);
            renderPolecatsPanel(sessions);
            renderActivityPanel(events);
            renderMailAllPanel(mail);
            renderEscalationsPanel(escalations);
            renderServicesPanel(services);
            renderRigsPanel(rigs, sessions, agents);
            renderDogsPanel(sessions);
            renderQueuesPanel(allBeads);
            renderBeadsPanel(allBeads, rigs);
            renderAssignedPanel(assigned);
            updateMayorBanner(sessions);
            updateSummaryBanner({
                sessions: sessions,
                beadsOpen: beadsOpen,
                beadsInProgress: beadsInProgress,
                convoys: convoys,
                escalations: escalations
            });
            updateCityTabs(_citiesList);

            // Load threaded inbox view.
            loadMailInbox(mail);
        });
    }

    function loadMailInbox(mailData) {
        var loading = document.getElementById('mail-loading');
        var threadsContainer = document.getElementById('mail-threads');
        var empty = document.getElementById('mail-empty');
        var count = document.getElementById('mail-count');

        if (!threadsContainer) return;

        var doRender = function(messages) {
            if (loading) loading.style.display = 'none';
            var threads = groupMailIntoThreads(messages);

            if (threads.length === 0) {
                threadsContainer.style.display = 'none';
                if (empty) empty.style.display = 'block';
                if (count) count.textContent = '0';
                return;
            }

            threadsContainer.style.display = 'block';
            if (empty) empty.style.display = 'none';

            var unreadTotal = 0;
            threads.forEach(function(t) { unreadTotal += t.unread_count; });
            if (count) {
                count.textContent = unreadTotal > 0 ? unreadTotal + ' unread' : String(threads.length);
                if (unreadTotal > 0) count.classList.add('has-unread');
                else count.classList.remove('has-unread');
            }

            var html = '';
            for (var i = 0; i < threads.length; i++) {
                var t = threads[i];
                var last = t.last_message || {};
                var hasMultiple = t.count > 1;
                var unreadClass = t.unread_count > 0 ? ' mail-thread-unread' : '';
                var countBadge = hasMultiple ? '<span class="thread-count">' + t.count + '</span>' : '';
                var unreadDot = t.unread_count > 0 ? '<span class="thread-unread-dot"></span>' : '';

                var priorityIcon = '';
                if (last.priority === 0 || last.priority === 'urgent') priorityIcon = '<span class="priority-urgent">\u26A1</span> ';
                else if (last.priority === 1 || last.priority === 'high') priorityIcon = '<span class="priority-high">!</span> ';

                var timeStr = last.created_at ? formatTimestamp(last.created_at) : '';
                var relativeStr = last.created_at ? ' (' + formatAge(last.created_at) + ')' : '';

                html += '<div class="mail-thread' + unreadClass + '">' +
                    '<div class="mail-thread-header" data-thread-id="' + escapeHtml(t.thread_id) + '"' +
                    (hasMultiple ? '' : ' data-msg-id="' + escapeHtml(last.id || '') + '" data-from="' + escapeHtml(last.from || '') + '"') + '>' +
                    '<div class="mail-thread-left">' + unreadDot +
                    '<span class="mail-from">' + escapeHtml(formatAgentAddress(last.from)) + '</span>' + countBadge +
                    '</div>' +
                    '<div class="mail-thread-center">' + priorityIcon +
                    '<span class="mail-subject">' + escapeHtml(t.subject || '') + '</span>' +
                    (hasMultiple && last.body ? '<span class="mail-thread-preview"> \u2014 ' + escapeHtml(last.body.substring(0, 60)) + '</span>' : '') +
                    '</div>' +
                    '<div class="mail-thread-right"><span class="mail-time">' + escapeHtml(timeStr + relativeStr) + '</span></div>' +
                    '</div>';

                if (hasMultiple) {
                    html += '<div class="mail-thread-messages" style="display: none;">';
                    for (var j = 0; j < t.messages.length; j++) {
                        var msg = t.messages[j];
                        var msgUnread = msg.read ? '' : ' mail-unread';
                        html += '<div class="mail-thread-msg' + msgUnread + '" data-msg-id="' + escapeHtml(msg.id) + '" data-from="' + escapeHtml(msg.from || '') + '">' +
                            '<div class="mail-thread-msg-header">' +
                            '<span class="mail-from">' + escapeHtml(formatAgentAddress(msg.from)) + '</span>' +
                            '<span class="mail-time">' + escapeHtml(msg.created_at ? formatTimestamp(msg.created_at) : '') + '</span>' +
                            '</div>' +
                            '<div class="mail-thread-msg-subject">' + escapeHtml(msg.subject || '') + '</div>' +
                            '</div>';
                    }
                    html += '</div>';
                }

                html += '</div>';
            }
            threadsContainer.innerHTML = html;
        };

        if (mailData) {
            doRender(mailData);
        } else {
            wsRequest('mail.list')
                .then(function(data) {
                    doRender(data.items || []);
                })
                .catch(function(err) {
                    if (loading) loading.textContent = 'Failed to load mail';
                    handleError(err, 'loadMailInbox');
                });
        }
    }

    // ================================================================
    // Section 5: Interactive Handlers
    // ================================================================

    // --- Convoy Detail ---
    var currentConvoyId = null;

    function openConvoyDetail(convoyId) {
        currentConvoyId = convoyId;
        window.pauseRefresh = true;

        var convoyList = document.getElementById('convoy-list');
        var convoyDetail = document.getElementById('convoy-detail');
        var convoyCreateForm = document.getElementById('convoy-create-form');

        document.getElementById('convoy-detail-id').textContent = convoyId;
        document.getElementById('convoy-detail-title').textContent = 'Convoy: ' + convoyId;
        document.getElementById('convoy-detail-status').textContent = '';
        document.getElementById('convoy-detail-progress').textContent = '';
        document.getElementById('convoy-issues-loading').style.display = 'block';
        document.getElementById('convoy-issues-table').style.display = 'none';
        document.getElementById('convoy-issues-empty').style.display = 'none';
        document.getElementById('convoy-add-issue-form').style.display = 'none';

        if (convoyList) convoyList.style.display = 'none';
        if (convoyCreateForm) convoyCreateForm.style.display = 'none';
        if (convoyDetail) convoyDetail.style.display = 'block';

        wsRequest('convoy.get', {id: convoyId})
            .then(function(data) {
                document.getElementById('convoy-issues-loading').style.display = 'none';

                // Update convoy status/progress.
                if (data && data.convoy) {
                    var sc = data.convoy.status || 'open';
                    var statusEl = document.getElementById('convoy-detail-status');
                    statusEl.textContent = sc;
                    statusEl.className = 'badge ' + statusBadgeClass(sc);
                }
                if (data && data.progress) {
                    document.getElementById('convoy-detail-progress').textContent =
                        data.progress.closed + '/' + data.progress.total + ' complete';
                }

                var issues = (data && data.children) || [];
                if (issues.length === 0) {
                    document.getElementById('convoy-issues-empty').style.display = 'block';
                    return;
                }

                var tbody = document.getElementById('convoy-issues-tbody');
                var html = '';
                for (var i = 0; i < issues.length; i++) {
                    var issue = issues[i];
                    var statusLower = (issue.status || '').toLowerCase();
                    var statusBadge = '<span class="badge ' + statusBadgeClass(statusLower) + '">' + escapeHtml(issue.status || 'Unknown') + '</span>';
                    var assigneeCell = issue.assignee
                        ? '<span class="badge badge-blue">' + escapeHtml(formatAgentAddress(issue.assignee)) + '</span>'
                        : '<span class="badge badge-muted">Unassigned</span>';

                    html += '<tr>' +
                        '<td>' + statusBadge + '</td>' +
                        '<td><span class="issue-id">' + escapeHtml(issue.id) + '</span></td>' +
                        '<td class="issue-title">' + escapeHtml(issue.title || '') + '</td>' +
                        '<td>' + assigneeCell + '</td>' +
                        '<td>' + escapeHtml(issue.progress || '') + '</td>' +
                        '</tr>';
                }
                tbody.innerHTML = html;
                document.getElementById('convoy-issues-table').style.display = 'table';
            })
            .catch(function(err) {
                document.getElementById('convoy-issues-loading').style.display = 'none';
                document.getElementById('convoy-issues-empty').style.display = 'block';
                var p = document.querySelector('#convoy-issues-empty p');
                if (p) p.textContent = 'Error: ' + err.message;
            });
    }

    function closeConvoyDetail() {
        var convoyList = document.getElementById('convoy-list');
        var convoyDetail = document.getElementById('convoy-detail');
        var convoyCreateForm = document.getElementById('convoy-create-form');
        if (convoyList) convoyList.style.display = 'block';
        if (convoyDetail) convoyDetail.style.display = 'none';
        if (convoyCreateForm) convoyCreateForm.style.display = 'none';
        currentConvoyId = null;
        window.pauseRefresh = false;
    }

    on('convoy-back-btn', 'click', closeConvoyDetail);

    // New convoy form.
    on('new-convoy-btn', 'click', function() {
        var convoyList = document.getElementById('convoy-list');
        var convoyDetail = document.getElementById('convoy-detail');
        var convoyCreateForm = document.getElementById('convoy-create-form');
        if (convoyList) convoyList.style.display = 'none';
        if (convoyDetail) convoyDetail.style.display = 'none';
        if (convoyCreateForm) convoyCreateForm.style.display = 'block';
        window.pauseRefresh = true;
        var nameInput = document.getElementById('convoy-create-name');
        if (nameInput) setTimeout(function() { nameInput.focus(); }, 50);
    });

    on('convoy-create-back-btn', 'click', function() {
        closeConvoyDetail();
    });

    on('convoy-create-cancel-btn', 'click', function() {
        closeConvoyDetail();
    });

    on('convoy-create-submit-btn', 'click', function() {
        var nameInput = document.getElementById('convoy-create-name');
        var issuesInput = document.getElementById('convoy-create-issues');
        var title = nameInput ? nameInput.value.trim() : '';
        var issuesStr = issuesInput ? issuesInput.value.trim() : '';

        if (!title) {
            showToast('error', 'Missing', 'Convoy name is required');
            return;
        }

        var items = issuesStr ? issuesStr.split(/\s+/) : [];
        wsRequest('convoy.create', {title: title, items: items})
            .then(function() {
                showToast('success', 'Created', 'Convoy "' + title + '" created');
                closeConvoyDetail();
                loadDashboard();
            })
            .catch(function(err) {
                showToast('error', 'Error', err.message);
            });
    });

    // Add issue to convoy.
    on('convoy-add-issue-btn', 'click', function() {
        var form = document.getElementById('convoy-add-issue-form');
        if (form) form.style.display = 'block';
        var input = document.getElementById('convoy-add-issue-input');
        if (input) setTimeout(function() { input.focus(); }, 50);
    });

    on('convoy-add-issue-cancel', 'click', function() {
        var form = document.getElementById('convoy-add-issue-form');
        if (form) form.style.display = 'none';
    });

    on('convoy-add-issue-submit', 'click', function() {
        var input = document.getElementById('convoy-add-issue-input');
        var issueId = input ? input.value.trim() : '';
        if (!issueId || !currentConvoyId) {
            showToast('error', 'Missing', 'Issue ID is required');
            return;
        }
        wsRequest('convoy.add', {id: currentConvoyId, items: [issueId]})
            .then(function() {
                showToast('success', 'Added', 'Issue added to convoy');
                var form = document.getElementById('convoy-add-issue-form');
                if (form) form.style.display = 'none';
                openConvoyDetail(currentConvoyId);
            })
            .catch(function(err) {
                showToast('error', 'Error', err.message);
            });
    });

    // Click on convoy row to view details (event delegation).
    document.addEventListener('click', function(e) {
        var row = e.target.closest('.convoy-row');
        if (row && row.hasAttribute('data-convoy-id')) {
            e.preventDefault();
            openConvoyDetail(row.getAttribute('data-convoy-id'));
        }
    });

    // --- Issue/Bead Detail ---
    var currentIssueId = null;

    function openIssueDetail(issueId) {
        currentIssueId = issueId;
        window.pauseRefresh = true;

        var issuesList = document.getElementById('issues-list');
        var issueDetail = document.getElementById('issue-detail');
        if (issuesList) issuesList.style.display = 'none';
        if (issueDetail) issueDetail.style.display = 'block';

        wsRequest('bead.get', {id: issueId})
            .then(function(bead) {
                if (!bead) return;

                var priEl = document.getElementById('issue-detail-priority');
                if (priEl) {
                    priEl.innerHTML = priorityBadge(bead.priority);
                }

                var idEl = document.getElementById('issue-detail-id');
                if (idEl) idEl.textContent = bead.id;

                var statusEl = document.getElementById('issue-detail-status');
                if (statusEl) {
                    statusEl.textContent = bead.status || 'open';
                    statusEl.className = 'issue-status badge ' + statusBadgeClass(bead.status || 'open');
                }

                var titleEl = document.getElementById('issue-detail-title-text');
                if (titleEl) titleEl.textContent = bead.title || '';

                var typeEl = document.getElementById('issue-detail-type');
                if (typeEl) typeEl.textContent = 'Type: ' + (bead.type || 'task');

                var ownerEl = document.getElementById('issue-detail-owner');
                if (ownerEl) ownerEl.textContent = 'Assignee: ' + (bead.assignee ? formatAgentAddress(bead.assignee) : 'none');

                var createdEl = document.getElementById('issue-detail-created');
                if (createdEl) createdEl.textContent = 'Created: ' + (bead.created_at ? formatAge(bead.created_at) + ' ago' : 'unknown');

                var descEl = document.getElementById('issue-detail-description');
                if (descEl) descEl.textContent = bead.description || 'No description.';

                // Dependencies.
                var depsSection = document.getElementById('issue-detail-deps');
                var depsDiv = document.getElementById('issue-detail-depends-on');
                if (depsSection && depsDiv) {
                    var deps = bead.depends_on || [];
                    if (deps.length > 0) {
                        depsSection.style.display = '';
                        depsDiv.innerHTML = deps.map(function(d) {
                            return '<span class="badge badge-muted">' + escapeHtml(d) + '</span>';
                        }).join(' ');
                    } else {
                        depsSection.style.display = 'none';
                    }
                }

                // Blocks.
                var blocksSection = document.getElementById('issue-detail-blocks-section');
                var blocksDiv = document.getElementById('issue-detail-blocks');
                if (blocksSection && blocksDiv) {
                    var blocks = bead.blocks || [];
                    if (blocks.length > 0) {
                        blocksSection.style.display = '';
                        blocksDiv.innerHTML = blocks.map(function(d) {
                            return '<span class="badge badge-muted">' + escapeHtml(d) + '</span>';
                        }).join(' ');
                    } else {
                        blocksSection.style.display = 'none';
                    }
                }

                // Build action buttons.
                var actionsDiv = document.getElementById('issue-detail-actions');
                if (actionsDiv) {
                    var actHtml = '';
                    if (bead.status === 'open') {
                        actHtml += '<button class="btn-primary issue-action-btn" data-action="close" data-id="' + escapeHtml(bead.id) + '">Close</button> ';
                    } else if (bead.status === 'closed') {
                        actHtml += '<button class="btn-primary issue-action-btn" data-action="reopen" data-id="' + escapeHtml(bead.id) + '">Reopen</button> ';
                    } else {
                        actHtml += '<button class="btn-primary issue-action-btn" data-action="close" data-id="' + escapeHtml(bead.id) + '">Close</button> ';
                    }

                    // Priority dropdown.
                    actHtml += '<select class="issue-priority-select" data-id="' + escapeHtml(bead.id) + '">' +
                        '<option value="1"' + (bead.priority === 1 ? ' selected' : '') + '>P1</option>' +
                        '<option value="2"' + (bead.priority === 2 ? ' selected' : '') + '>P2</option>' +
                        '<option value="3"' + (bead.priority === 3 ? ' selected' : '') + '>P3</option>' +
                        '<option value="4"' + (bead.priority === 4 ? ' selected' : '') + '>P4</option>' +
                        '</select> ';

                    // Sling button.
                    actHtml += '<button class="sling-btn" data-bead-id="' + escapeHtml(bead.id) + '">Sling</button>';

                    actionsDiv.innerHTML = actHtml;
                }
            })
            .catch(function(err) {
                showToast('error', 'Error', err.message);
            });
    }

    function closeIssueDetail() {
        var issuesList = document.getElementById('issues-list');
        var issueDetail = document.getElementById('issue-detail');
        if (issuesList) issuesList.style.display = 'block';
        if (issueDetail) issueDetail.style.display = 'none';
        currentIssueId = null;
        window.pauseRefresh = false;
    }

    on('issue-back-btn', 'click', closeIssueDetail);

    function closeIssue(id) {
        wsRequest('bead.close', {id: id}).then(function() {
            showToast('success', 'Closed', id + ' closed');
            closeIssueDetail();
            loadDashboard();
        }).catch(function(err) { showToast('error', 'Error', err.message); });
    }

    function reopenIssue(id) {
        wsRequest('bead.reopen', {id: id}).then(function() {
            showToast('success', 'Reopened', id + ' reopened');
            closeIssueDetail();
            loadDashboard();
        }).catch(function(err) { showToast('error', 'Error', err.message); });
    }

    function assignIssue(id, assignee) {
        wsRequest('bead.assign', {id: id, assignee: assignee}).then(function() {
            showToast('success', 'Assigned', id + ' assigned to ' + assignee);
            if (currentIssueId === id) openIssueDetail(id);
            loadDashboard();
        }).catch(function(err) { showToast('error', 'Error', err.message); });
    }

    function updateIssuePriority(id, priority) {
        wsRequest('bead.update', {id: id, priority: parseInt(priority, 10)}).then(function() {
            showToast('success', 'Updated', 'Priority changed to P' + priority);
            loadDashboard();
        }).catch(function(err) { showToast('error', 'Error', err.message); });
    }

    // Click on issue row to view details (event delegation).
    document.addEventListener('click', function(e) {
        var row = e.target.closest('.issue-row');
        if (row && row.hasAttribute('data-bead-id') && !e.target.closest('.sling-btn')) {
            e.preventDefault();
            openIssueDetail(row.getAttribute('data-bead-id'));
        }
    });

    // Issue action buttons (close, reopen) via event delegation.
    document.addEventListener('click', function(e) {
        var btn = e.target.closest('.issue-action-btn');
        if (!btn) return;
        e.preventDefault();
        var action = btn.getAttribute('data-action');
        var id = btn.getAttribute('data-id');
        if (action === 'close') closeIssue(id);
        else if (action === 'reopen') reopenIssue(id);
    });

    // Issue priority change via event delegation.
    document.addEventListener('change', function(e) {
        if (e.target.classList.contains('issue-priority-select')) {
            var id = e.target.getAttribute('data-id');
            var priority = e.target.value;
            updateIssuePriority(id, priority);
        }
    });

    // Issue creation modal.
    function openIssueModal() {
        var modal = document.getElementById('issue-modal');
        if (modal) {
            modal.style.display = 'flex';
            window.pauseRefresh = true;
            var titleInput = document.getElementById('issue-title');
            if (titleInput) setTimeout(function() { titleInput.focus(); }, 100);
        }
    }
    window.openIssueModal = openIssueModal;

    function closeIssueModal() {
        var modal = document.getElementById('issue-modal');
        if (modal) {
            modal.style.display = 'none';
            window.pauseRefresh = false;
            var form = document.getElementById('issue-form');
            if (form) form.reset();
        }
    }
    window.closeIssueModal = closeIssueModal;

    function submitIssue(e) {
        e.preventDefault();
        var title = document.getElementById('issue-title').value.trim();
        var priority = document.getElementById('issue-priority').value;
        var description = document.getElementById('issue-description').value.trim();
        var submitBtn = document.getElementById('issue-submit-btn');

        if (!title) {
            showToast('error', 'Missing', 'Title is required');
            return;
        }

        if (submitBtn) {
            submitBtn.disabled = true;
            submitBtn.textContent = 'Creating...';
        }

        var payload = {
            title: title,
            priority: parseInt(priority, 10)
        };
        if (description) payload.description = description;

        wsRequest('bead.create', payload)
            .then(function(data) {
                showToast('success', 'Created', 'Issue ' + (data && data.id ? data.id : '') + ' created');
                closeIssueModal();
                loadDashboard();
            })
            .catch(function(err) {
                showToast('error', 'Error', err.message);
            })
            .finally(function() {
                if (submitBtn) {
                    submitBtn.disabled = false;
                    submitBtn.textContent = 'Create Issue';
                }
            });
    }
    window.submitIssue = submitIssue;

    // Close modal on Escape.
    document.addEventListener('keydown', function(e) {
        if (e.key === 'Escape') {
            var modal = document.getElementById('issue-modal');
            if (modal && modal.style.display !== 'none') {
                closeIssueModal();
            }
        }
    });

    // --- Mail Detail + Compose ---
    var currentMessageId = null;
    var currentMessageFrom = null;
    var currentMailTab = 'inbox';

    // Mail tab switching.
    document.querySelectorAll('.mail-tab').forEach(function(tab) {
        tab.addEventListener('click', function() {
            var targetTab = tab.getAttribute('data-tab');
            if (targetTab === currentMailTab) return;

            document.querySelectorAll('.mail-tab').forEach(function(t) { t.classList.remove('active'); });
            tab.classList.add('active');
            currentMailTab = targetTab;

            var mailList = document.getElementById('mail-list');
            var mailAll = document.getElementById('mail-all');
            var mailDetail = document.getElementById('mail-detail');
            var mailCompose = document.getElementById('mail-compose');

            if (targetTab === 'inbox') {
                if (mailList) mailList.style.display = 'block';
                if (mailAll) mailAll.style.display = 'none';
            } else {
                if (mailList) mailList.style.display = 'none';
                if (mailAll) mailAll.style.display = 'block';
            }
            if (mailDetail) mailDetail.style.display = 'none';
            if (mailCompose) mailCompose.style.display = 'none';
        });
    });

    function openMailDetail(msgId, from) {
        currentMessageId = msgId;
        currentMessageFrom = from;
        window.pauseRefresh = true;

        var mailList = document.getElementById('mail-list');
        var mailAll = document.getElementById('mail-all');
        var mailDetail = document.getElementById('mail-detail');
        var mailCompose = document.getElementById('mail-compose');

        if (mailList) mailList.style.display = 'none';
        if (mailAll) mailAll.style.display = 'none';
        if (mailCompose) mailCompose.style.display = 'none';
        if (mailDetail) mailDetail.style.display = 'block';

        // Populate from local message data or fetch.
        wsRequest('mail.get', {id: msgId})
            .then(function(msg) {
                if (!msg) return;
                var subjectEl = document.getElementById('mail-detail-subject');
                if (subjectEl) subjectEl.textContent = msg.subject || '';
                var fromEl = document.getElementById('mail-detail-from');
                if (fromEl) fromEl.textContent = formatAgentAddress(msg.from);
                var timeEl = document.getElementById('mail-detail-time');
                if (timeEl) timeEl.textContent = msg.created_at ? formatTimestamp(msg.created_at) : '';
                var bodyEl = document.getElementById('mail-detail-body');
                if (bodyEl) bodyEl.textContent = msg.body || '';
            })
            .catch(function(err) {
                var bodyEl = document.getElementById('mail-detail-body');
                if (bodyEl) bodyEl.textContent = 'Error loading message: ' + err.message;
            });
    }

    function closeMailDetail() {
        var mailList = document.getElementById('mail-list');
        var mailAll = document.getElementById('mail-all');
        var mailDetail = document.getElementById('mail-detail');
        if (mailDetail) mailDetail.style.display = 'none';
        if (currentMailTab === 'inbox') {
            if (mailList) mailList.style.display = 'block';
        } else {
            if (mailAll) mailAll.style.display = 'block';
        }
        window.pauseRefresh = false;
    }

    on('mail-back-btn', 'click', closeMailDetail);

    // Reply button.
    on('mail-reply-btn', 'click', function() {
        openComposeForm(currentMessageFrom, 'Re: ' + (document.getElementById('mail-detail-subject').textContent || ''), currentMessageId);
    });

    // Compose button.
    on('compose-mail-btn', 'click', function() {
        openComposeForm('', '', '');
    });

    function openComposeForm(to, subject, replyToId) {
        var mailList = document.getElementById('mail-list');
        var mailAll = document.getElementById('mail-all');
        var mailDetail = document.getElementById('mail-detail');
        var mailCompose = document.getElementById('mail-compose');

        if (mailList) mailList.style.display = 'none';
        if (mailAll) mailAll.style.display = 'none';
        if (mailDetail) mailDetail.style.display = 'none';
        if (mailCompose) mailCompose.style.display = 'block';

        window.pauseRefresh = true;

        var titleEl = document.getElementById('mail-compose-title');
        if (titleEl) titleEl.textContent = replyToId ? 'Reply' : 'New Message';

        var replyInput = document.getElementById('compose-reply-to');
        if (replyInput) replyInput.value = replyToId || '';

        var subjectInput = document.getElementById('compose-subject');
        if (subjectInput) subjectInput.value = subject || '';

        var bodyInput = document.getElementById('compose-body');
        if (bodyInput) bodyInput.value = '';

        populateToDropdown(to);

        if (!to && subjectInput) setTimeout(function() { subjectInput.focus(); }, 50);
    }

    function populateToDropdown(selected) {
        var toSelect = document.getElementById('compose-to');
        if (!toSelect) return;

        wsRequest('agents.list')
            .then(function(data) {
                var agents = data.items || [];
                var html = '<option value="">Select recipient...</option>';
                agents.forEach(function(a) {
                    var name = a.name || a.template || '';
                    var sel = (name === selected) ? ' selected' : '';
                    html += '<option value="' + escapeHtml(name) + '"' + sel + '>' + escapeHtml(name) + '</option>';
                });
                toSelect.innerHTML = html;
                if (selected) toSelect.value = selected;
            })
            .catch(function() {
                toSelect.innerHTML = '<option value="">No agents available</option>';
            });
    }

    function closeComposeForm() {
        var mailList = document.getElementById('mail-list');
        var mailAll = document.getElementById('mail-all');
        var mailCompose = document.getElementById('mail-compose');
        if (mailCompose) mailCompose.style.display = 'none';
        if (currentMailTab === 'inbox') {
            if (mailList) mailList.style.display = 'block';
        } else {
            if (mailAll) mailAll.style.display = 'block';
        }
        window.pauseRefresh = false;
    }

    on('compose-back-btn', 'click', closeComposeForm);
    on('compose-cancel-btn', 'click', closeComposeForm);

    on('mail-send-btn', 'click', function() {
        var toSelect = document.getElementById('compose-to');
        var subjectInput = document.getElementById('compose-subject');
        var bodyInput = document.getElementById('compose-body');
        var replyInput = document.getElementById('compose-reply-to');

        var to = toSelect ? toSelect.value : '';
        var subject = subjectInput ? subjectInput.value.trim() : '';
        var body = bodyInput ? bodyInput.value.trim() : '';
        var replyTo = replyInput ? replyInput.value : '';

        if (!to) {
            showToast('error', 'Missing', 'Recipient is required');
            return;
        }

        var action, payload;
        if (replyTo) {
            action = 'mail.reply';
            payload = {id: replyTo, body: body};
        } else {
            action = 'mail.send';
            payload = {to: to, subject: subject, body: body};
        }

        wsRequest(action, payload)
            .then(function() {
                showToast('success', 'Sent', 'Message sent');
                closeComposeForm();
                loadDashboard();
            })
            .catch(function(err) {
                showToast('error', 'Error', err.message);
            });
    });

    // Click on mail thread/message (event delegation).
    document.addEventListener('click', function(e) {
        // Single-message thread click.
        var header = e.target.closest('.mail-thread-header');
        if (header) {
            var msgId = header.getAttribute('data-msg-id');
            var from = header.getAttribute('data-from');
            if (msgId) {
                e.preventDefault();
                openMailDetail(msgId, from);
                return;
            }
            // Multi-message thread: toggle messages.
            var thread = header.closest('.mail-thread');
            if (thread) {
                var msgs = thread.querySelector('.mail-thread-messages');
                if (msgs) {
                    msgs.style.display = msgs.style.display === 'none' ? 'block' : 'none';
                }
            }
            return;
        }
        // Individual message in expanded thread.
        var msgEl = e.target.closest('.mail-thread-msg');
        if (msgEl) {
            e.preventDefault();
            openMailDetail(msgEl.getAttribute('data-msg-id'), msgEl.getAttribute('data-from'));
            return;
        }
        // Mail "all traffic" row click.
        var mailRow = e.target.closest('.mail-row');
        if (mailRow) {
            e.preventDefault();
            openMailDetail(mailRow.getAttribute('data-msg-id'), mailRow.getAttribute('data-from'));
        }
    });

    // --- Escalation Actions (event delegation) ---
    document.addEventListener('click', function(e) {
        var btn = e.target.closest('.escalation-action-btn');
        if (!btn) return;
        e.preventDefault();
        var action = btn.getAttribute('data-action');
        var id = btn.getAttribute('data-id');
        if (!action || !id) return;

        if (action === 'ack') {
            wsRequest('bead.update', {id: id, labels: ['acked']})
                .then(function() {
                    showToast('success', 'Acknowledged', id + ' acknowledged');
                    loadDashboard();
                })
                .catch(function(err) { showToast('error', 'Error', err.message); });
        } else if (action === 'resolve') {
            wsRequest('bead.close', {id: id})
                .then(function() {
                    showToast('success', 'Resolved', id + ' resolved');
                    loadDashboard();
                })
                .catch(function(err) { showToast('error', 'Error', err.message); });
        } else if (action === 'reassign') {
            // Show a simple prompt for reassignment.
            wsRequest('agents.list')
                .then(function(data) {
                    var agents = (data.items || []).map(function(a) { return a.name || a.template || ''; }).filter(Boolean);
                    if (agents.length === 0) {
                        showToast('info', 'No agents', 'No agents available to reassign');
                        return;
                    }
                    var choice = prompt('Reassign to agent:\n' + agents.join(', '));
                    if (choice && agents.indexOf(choice) !== -1) {
                        wsRequest('bead.assign', {id: id, assignee: choice})
                            .then(function() {
                                showToast('success', 'Reassigned', id + ' reassigned to ' + choice);
                                loadDashboard();
                            })
                            .catch(function(err) { showToast('error', 'Error', err.message); });
                    }
                })
                .catch(function(err) { showToast('error', 'Error', err.message); });
        }
    });

    // --- Sling (event delegation) ---
    document.addEventListener('click', function(e) {
        var btn = e.target.closest('.sling-btn');
        if (!btn) return;
        e.preventDefault();
        var beadId = btn.getAttribute('data-bead-id');
        if (!beadId) return;

        // Fetch rigs and show a dropdown.
        wsRequest('rigs.list')
            .then(function(data) {
                var rigs = (data.items || []).map(function(r) { return r.name || ''; }).filter(Boolean);
                if (rigs.length === 0) {
                    showToast('info', 'No rigs', 'No rigs available');
                    return;
                }
                if (rigs.length === 1) {
                    return doSling(beadId, rigs[0]);
                }
                var choice = prompt('Select rig:\n' + rigs.join(', '));
                if (choice && rigs.indexOf(choice) !== -1) {
                    return doSling(beadId, choice);
                }
            })
            .catch(function(err) { showToast('error', 'Error', err.message); });
    });

    function doSling(beadId, rig) {
        return wsRequest('sling.run', {bead_id: beadId, rig: rig})
            .then(function() {
                showToast('success', 'Slung', beadId + ' slung to ' + rig);
                loadDashboard();
            })
            .catch(function(err) { showToast('error', 'Error', err.message); });
    }

    // --- Assigned Management ---
    function openAssignForm() {
        var form = document.getElementById('assign-form');
        if (form) {
            form.style.display = 'block';
            var input = document.getElementById('assign-bead');
            if (input) {
                input.value = '';
                setTimeout(function() { input.focus(); }, 50);
            }
        }
    }
    window.openAssignForm = openAssignForm;

    function closeAssignForm() {
        var form = document.getElementById('assign-form');
        if (form) form.style.display = 'none';
    }
    window.closeAssignForm = closeAssignForm;

    function submitAssign() {
        var input = document.getElementById('assign-bead');
        var beadId = input ? input.value.trim() : '';
        if (!beadId) {
            showToast('error', 'Missing', 'Bead ID is required');
            return;
        }

        var submitBtn = document.querySelector('.assign-submit');
        if (submitBtn) {
            submitBtn.disabled = true;
            submitBtn.textContent = '...';
        }

        wsRequest('sling.run', {bead_id: beadId})
            .then(function() {
                showToast('success', 'Assigned', beadId + ' assigned');
                closeAssignForm();
                loadDashboard();
            })
            .catch(function(err) {
                showToast('error', 'Failed', err.message);
            })
            .finally(function() {
                if (submitBtn) {
                    submitBtn.disabled = false;
                    submitBtn.textContent = 'Assign';
                }
            });
    }
    window.submitAssign = submitAssign;

    function unassignBead(btn) {
        var beadId = btn.getAttribute('data-bead-id');
        if (!beadId) return;
        if (!confirm('Unassign ' + beadId + '?')) return;

        btn.disabled = true;
        btn.textContent = '...';

        wsRequest('bead.assign', {id: beadId, assignee: ''})
            .then(function() {
                showToast('success', 'Unassigned', beadId + ' unassigned');
                loadDashboard();
            })
            .catch(function(err) {
                showToast('error', 'Failed', err.message);
                btn.disabled = false;
                btn.textContent = 'Unassign';
            });
    }
    window.unassignBead = unassignBead;

    // Unassign button event delegation.
    document.addEventListener('click', function(e) {
        var btn = e.target.closest('.unassign-btn');
        if (btn) {
            e.preventDefault();
            unassignBead(btn);
        }
    });

    function clearAllAssigned() {
        if (!confirm('Unassign ALL? This will unassign all active work.')) return;

        var rows = document.querySelectorAll('.unassign-btn');
        if (rows.length === 0) {
            showToast('info', 'Nothing', 'No assigned work to clear');
            return;
        }

        var beadIds = [];
        for (var i = 0; i < rows.length; i++) {
            var id = rows[i].getAttribute('data-bead-id');
            if (id) beadIds.push(id);
        }

        var completed = 0;
        var errors = 0;

        beadIds.forEach(function(beadId) {
            wsRequest('bead.assign', {id: beadId, assignee: ''})
                .then(function() { completed++; })
                .catch(function() { errors++; })
                .finally(function() {
                    if (completed + errors === beadIds.length) {
                        if (errors > 0) {
                            showToast('error', 'Partial', completed + ' unassigned, ' + errors + ' failed');
                        } else {
                            showToast('success', 'Cleared', completed + ' assignment(s) cleared');
                        }
                        loadDashboard();
                    }
                });
        });
    }
    window.clearAllAssigned = clearAllAssigned;

    // Handle Enter/Escape in assign input.
    document.addEventListener('keydown', function(e) {
        if (e.key === 'Enter' && e.target.id === 'assign-bead') {
            e.preventDefault();
            submitAssign();
        }
        if (e.key === 'Escape' && e.target.id === 'assign-bead') {
            e.preventDefault();
            closeAssignForm();
        }
    });

    // --- Agent Log Drawer ---
    var _logDrawerSessionId = null;

    function openLogDrawer(agentName) {
        var drawer = document.getElementById('agent-log-drawer');
        var nameEl = document.getElementById('log-drawer-agent-name');
        var messagesEl = document.getElementById('log-drawer-messages');
        var loadingEl = document.getElementById('log-drawer-loading');
        var countEl = document.getElementById('log-drawer-count');
        var statusEl = document.getElementById('log-drawer-status');

        if (!drawer) return;
        drawer.style.display = 'block';
        if (nameEl) nameEl.textContent = agentName;
        if (messagesEl) messagesEl.innerHTML = '';
        if (loadingEl) loadingEl.style.display = 'block';
        if (countEl) countEl.textContent = '0';
        if (statusEl) statusEl.textContent = 'Loading...';

        // Find session for this agent.
        wsRequest('sessions.list', {state: 'active', peek: true})
            .then(function(data) {
                var sessions = data.items || [];
                var session = sessions.find(function(s) {
                    return (s.template || s.title || '') === agentName;
                });
                if (!session) {
                    if (loadingEl) loadingEl.textContent = 'No session found for ' + agentName;
                    return;
                }

                _logDrawerSessionId = session.id;
                return wsRequest('session.transcript', {id: session.id});
            })
            .then(function(data) {
                if (!data) return;
                if (loadingEl) loadingEl.style.display = 'none';
                if (statusEl) statusEl.textContent = '';

                var messages = [];
                if (typeof data === 'string') {
                    messages = data.split('\n').filter(Boolean);
                } else if (Array.isArray(data)) {
                    messages = data;
                } else if (data.messages) {
                    messages = data.messages;
                } else if (data.transcript) {
                    messages = typeof data.transcript === 'string' ? data.transcript.split('\n').filter(Boolean) : data.transcript;
                }

                if (countEl) countEl.textContent = String(messages.length);

                var html = '';
                for (var i = 0; i < messages.length; i++) {
                    var msg = typeof messages[i] === 'string' ? messages[i] : (messages[i].text || messages[i].content || JSON.stringify(messages[i]));
                    html += '<div class="log-message">' + escapeHtml(msg) + '</div>';
                }
                if (messagesEl) messagesEl.innerHTML = html;

                // Scroll to bottom.
                var body = document.getElementById('log-drawer-body');
                if (body) body.scrollTop = body.scrollHeight;

                // Show "load older" button.
                var olderBtn = document.getElementById('log-drawer-older-btn');
                if (olderBtn && messages.length >= 50) {
                    olderBtn.style.display = 'inline-block';
                }
            })
            .catch(function(err) {
                if (loadingEl) loadingEl.textContent = 'Error: ' + err.message;
                if (statusEl) statusEl.textContent = 'Error';
            });
    }

    on('log-drawer-close-btn', 'click', function() {
        var drawer = document.getElementById('agent-log-drawer');
        if (drawer) drawer.style.display = 'none';
        _logDrawerSessionId = null;
    });

    on('log-drawer-older-btn', 'click', function() {
        if (!_logDrawerSessionId) return;
        // Fetch older transcript messages.
        var messagesEl = document.getElementById('log-drawer-messages');
        var existingCount = messagesEl ? messagesEl.children.length : 0;
        wsRequest('session.transcript', {id: _logDrawerSessionId, before: existingCount})
            .then(function(data) {
                if (!data) return;
                var messages = [];
                if (typeof data === 'string') {
                    messages = data.split('\n').filter(Boolean);
                } else if (Array.isArray(data)) {
                    messages = data;
                } else if (data.messages) {
                    messages = data.messages;
                }
                var html = '';
                for (var i = 0; i < messages.length; i++) {
                    var msg = typeof messages[i] === 'string' ? messages[i] : (messages[i].text || messages[i].content || JSON.stringify(messages[i]));
                    html += '<div class="log-message">' + escapeHtml(msg) + '</div>';
                }
                if (messagesEl) messagesEl.insertAdjacentHTML('afterbegin', html);
            })
            .catch(function(err) { handleError(err, 'log-drawer-older'); });
    });

    // Agent log link event delegation.
    document.addEventListener('click', function(e) {
        var link = e.target.closest('.agent-log-link');
        if (!link) return;
        e.preventDefault();
        var agentName = link.getAttribute('data-agent-name');
        if (agentName) openLogDrawer(agentName);
    });

    // Handle attach button clicks - copy command to clipboard.
    document.addEventListener('click', function(e) {
        var btn = e.target.closest('.attach-btn');
        if (!btn) return;
        e.preventDefault();
        var cmd = btn.getAttribute('data-cmd');
        if (!cmd) return;
        navigator.clipboard.writeText(cmd).then(function() {
            showToast('success', 'Copied', cmd);
        }).catch(function() {
            showToast('info', 'Run in terminal', cmd);
        });
    });

    // ================================================================
    // Section 6: Command Palette
    // ================================================================

    var allCommands = [];
    var visibleCommands = [];
    var selectedIdx = 0;
    var isPaletteOpen = false;
    var executionLock = false;
    var pendingCommand = null;
    var cachedOptions = null;
    var recentCommands = [];
    var MAX_RECENT = 10;
    var RECENT_STORAGE_KEY = 'gt-palette-recent';

    // Load recent commands from localStorage.
    function loadRecentCommands() {
        try {
            var stored = localStorage.getItem(RECENT_STORAGE_KEY);
            if (stored) {
                recentCommands = JSON.parse(stored);
                if (!Array.isArray(recentCommands)) recentCommands = [];
                recentCommands = recentCommands.slice(0, MAX_RECENT);
            }
        } catch (e) {
            recentCommands = [];
        }
    }

    function saveRecentCommand(cmdName) {
        recentCommands = recentCommands.filter(function(c) { return c !== cmdName; });
        recentCommands.unshift(cmdName);
        recentCommands = recentCommands.slice(0, MAX_RECENT);
        try {
            localStorage.setItem(RECENT_STORAGE_KEY, JSON.stringify(recentCommands));
        } catch (e) { /* ignore */ }
    }

    loadRecentCommands();

    // Build commands from hello capabilities.
    function _buildCommandsFromCapabilities(caps) {
        _wsCapabilities = caps || [];
        allCommands = [
            {name: 'status', desc: 'Show city status', category: 'System'},
            {name: 'mail inbox', desc: 'Show mail inbox', category: 'Mail'},
            {name: 'mail send', desc: 'Send mail', category: 'Mail', args: '<address> -s <subject> -m <message>', argType: 'agents'},
            {name: 'mail read', desc: 'Read a message', category: 'Mail', args: '<id>', argType: 'messages'},
            {name: 'mail archive', desc: 'Archive a message', category: 'Mail', args: '<id>', argType: 'messages'},
            {name: 'convoy list', desc: 'List convoys', category: 'Work'},
            {name: 'convoy create', desc: 'Create convoy', category: 'Work', args: '<name>'},
            {name: 'convoy status', desc: 'Show convoy detail', category: 'Work', args: '<id>', argType: 'convoys'},
            {name: 'rig list', desc: 'List rigs', category: 'System'},
            {name: 'agent list', desc: 'List agents', category: 'System'},
            {name: 'sling', desc: 'Sling a bead to a rig', category: 'Work', args: '<bead_id> <rig>', argType: 'hooks'},
            {name: 'unsling', desc: 'Unassign a bead', category: 'Work', args: '<bead_id>', argType: 'hooks'}
        ];

        // Add dynamic commands from capabilities.
        if (_wsCapabilities.indexOf('config.get') !== -1) {
            allCommands.push({name: 'config get', desc: 'Show city config', category: 'System'});
        }
        if (_wsCapabilities.indexOf('config.validate') !== -1) {
            allCommands.push({name: 'config validate', desc: 'Validate city config', category: 'System'});
        }
    }

    // Fetch dynamic options via WS.
    function fetchOptions() {
        return Promise.all([
            wsRequest('rigs.list').catch(function() { return {items: []}; }),
            wsRequest('sessions.list', {state: 'active'}).catch(function() { return {items: []}; }),
            wsRequest('beads.list', {status: 'open'}).catch(function() { return {items: []}; }),
            wsRequest('mail.list').catch(function() { return {items: []}; })
        ]).then(function(results) {
            cachedOptions = {
                rigs: (results[0].items || []).map(function(r) { return r.name || ''; }).filter(Boolean),
                agents: (results[1].items || []).map(function(s) { return s.template || s.id || ''; }).filter(Boolean),
                hooks: (results[2].items || []).map(function(b) { return b.id; }),
                messages: (results[3].items || []).map(function(m) { return m.id; }),
                convoys: []
            };
            return cachedOptions;
        }).catch(function() {
            handleError(new Error('Failed to load options'), 'fetchOptions');
            return null;
        });
    }

    function getOptionsForType(argType) {
        if (!cachedOptions) return [];
        var rawOptions;
        switch (argType) {
        case 'rigs': rawOptions = cachedOptions.rigs || []; break;
        case 'agents': rawOptions = cachedOptions.agents || []; break;
        case 'hooks': rawOptions = cachedOptions.hooks || []; break;
        case 'messages': rawOptions = cachedOptions.messages || []; break;
        case 'convoys': rawOptions = cachedOptions.convoys || []; break;
        default: return [];
        }
        return rawOptions.map(function(opt) {
            if (typeof opt === 'string') {
                return {value: opt, label: opt, disabled: false};
            }
            return {value: opt.name || '', label: opt.name || '', disabled: false};
        });
    }

    // Map CLI-style commands to WS actions.
    function dispatchCommandAsWSAction(cmdStr) {
        var parts = cmdStr.trim().split(/\s+/);
        var cmd = parts[0];
        var subcmd = parts[1] || '';
        var args = parts.slice(2);

        switch (cmd) {
        case 'status':
            return wsRequest('status.get');
        case 'sling':
            return wsRequest('sling.run', {bead_id: parts[1], rig: parts[2] || ''});
        case 'unsling':
            return wsRequest('bead.assign', {id: parts[1], assignee: ''});
        case 'mail':
            switch (subcmd) {
            case 'inbox': case 'check':
                return wsRequest('mail.list');
            case 'send':
                return wsRequest('mail.send', {to: args[0], subject: args.slice(1).join(' '), body: ''});
            case 'read': case 'mark-read':
                return wsRequest('mail.read', {id: args[0]});
            case 'archive':
                return wsRequest('mail.archive', {id: args[0]});
            case 'mark-unread':
                return wsRequest('mail.mark_unread', {id: args[0]});
            default:
                return Promise.reject(new Error('Unknown mail command: ' + subcmd));
            }
        case 'convoy':
            switch (subcmd) {
            case 'list':
                return wsRequest('convoys.list');
            case 'status': case 'show':
                return wsRequest('convoy.get', {id: args[0]});
            case 'create':
                return wsRequest('convoy.create', {title: args[0], items: args.slice(1)});
            case 'add':
                return wsRequest('convoy.add', {id: args[0], items: args.slice(1)});
            default:
                return Promise.reject(new Error('Unknown convoy command: ' + subcmd));
            }
        case 'rig':
            switch (subcmd) {
            case 'list':
                return wsRequest('rigs.list');
            default:
                return Promise.reject(new Error('Unknown rig command: ' + subcmd));
            }
        case 'agent':
            switch (subcmd) {
            case 'list':
                return wsRequest('agents.list');
            default:
                return Promise.reject(new Error('Unknown agent command: ' + subcmd));
            }
        case 'config':
            switch (subcmd) {
            case 'get':
                return wsRequest('config.get');
            case 'validate':
                return wsRequest('config.validate');
            default:
                return Promise.reject(new Error('Unknown config command: ' + subcmd));
            }
        default:
            return Promise.reject(new Error('Command not available over WebSocket: ' + cmd));
        }
    }

    // Fuzzy match scoring.
    function scoreCommand(cmd, query) {
        var name = cmd.name.toLowerCase();
        var desc = (cmd.desc || '').toLowerCase();
        var cat = (cmd.category || '').toLowerCase();
        var q = query.toLowerCase();

        if (name.indexOf(q) === 0) return 100 + (50 - name.length);
        var nameParts = name.split(' ');
        for (var i = 0; i < nameParts.length; i++) {
            if (nameParts[i].indexOf(q) === 0) return 80 + (50 - name.length);
        }
        if (name.indexOf(q) !== -1) return 60 + (50 - name.length);
        if (desc.indexOf(q) !== -1) return 40;
        if (cat.indexOf(q) !== -1) return 20;
        // Fuzzy: all query chars in order.
        var ni = 0;
        for (var qi = 0; qi < q.length; qi++) {
            ni = name.indexOf(q[qi], ni);
            if (ni === -1) return -1;
            ni++;
        }
        return 10;
    }

    function highlightMatch(text, query) {
        if (!query) return escapeHtml(text);
        var lowerText = text.toLowerCase();
        var lowerQuery = query.toLowerCase();
        var idx = lowerText.indexOf(lowerQuery);
        if (idx !== -1) {
            return escapeHtml(text.substring(0, idx)) +
                '<mark>' + escapeHtml(text.substring(idx, idx + query.length)) + '</mark>' +
                escapeHtml(text.substring(idx + query.length));
        }
        return escapeHtml(text);
    }

    function detectActiveContext() {
        var expandedPanel = document.querySelector('.panel.expanded');
        if (expandedPanel) {
            var panelId = expandedPanel.id || '';
            if (panelId.indexOf('mail') !== -1) return 'Mail';
            if (panelId.indexOf('crew') !== -1) return 'System';
            if (panelId.indexOf('bead') !== -1 || panelId.indexOf('issue') !== -1) return 'Work';
            if (panelId.indexOf('convoy') !== -1) return 'Work';
        }
        var mailDetail = document.getElementById('mail-detail');
        var mailCompose = document.getElementById('mail-compose');
        if ((mailDetail && mailDetail.style.display !== 'none') ||
            (mailCompose && mailCompose.style.display !== 'none')) return 'Mail';
        var issueDetail = document.getElementById('issue-detail');
        if (issueDetail && issueDetail.style.display !== 'none') return 'Work';
        return null;
    }

    function parseArgsTemplate(argsStr) {
        if (!argsStr) return [];
        var args = [];
        var regex = /(?:(-\w+)\s+)?<([^>]+)>/g;
        var match;
        while ((match = regex.exec(argsStr)) !== null) {
            args.push({name: match[2], flag: match[1] || null});
        }
        return args;
    }

    var overlay = document.getElementById('command-palette-overlay');
    var searchInput = document.getElementById('command-palette-input');
    var resultsDiv = document.getElementById('command-palette-results');

    // Output panel.
    var outputPanel = document.getElementById('output-panel');
    var outputContent = document.getElementById('output-panel-content');
    var outputCmd = document.getElementById('output-panel-cmd');

    function showOutput(cmd, output) {
        if (outputCmd) outputCmd.textContent = 'gc ' + cmd;
        if (outputContent) outputContent.textContent = typeof output === 'string' ? output : JSON.stringify(output, null, 2);
        if (outputPanel) outputPanel.classList.add('open');
    }

    on('output-close-btn', 'click', function() {
        if (outputPanel) outputPanel.classList.remove('open');
    });

    on('output-copy-btn', 'click', function() {
        if (outputContent) {
            navigator.clipboard.writeText(outputContent.textContent).then(function() {
                showToast('success', 'Copied', 'Output copied to clipboard');
            });
        }
    });

    function filterCommands(query) {
        query = (query || '').trim();
        if (!query) {
            visibleCommands = [];
            var shownNames = {};

            // Recent section.
            var recentItems = [];
            for (var ri = 0; ri < recentCommands.length; ri++) {
                var recentCmd = allCommands.find(function(c) { return c.name === recentCommands[ri]; });
                if (recentCmd) recentItems.push(recentCmd);
            }
            if (recentItems.length > 0) {
                visibleCommands.push({_section: 'Recent'});
                for (var ri2 = 0; ri2 < recentItems.length; ri2++) {
                    var rcmd = Object.assign({}, recentItems[ri2], {_recent: true});
                    visibleCommands.push(rcmd);
                    shownNames[rcmd.name] = true;
                }
            }

            // Contextual section.
            var context = detectActiveContext();
            if (context) {
                var contextItems = allCommands.filter(function(c) {
                    return c.category === context && !shownNames[c.name];
                });
                if (contextItems.length > 0) {
                    visibleCommands.push({_section: 'Suggested \u2014 ' + context});
                    for (var ci = 0; ci < contextItems.length; ci++) {
                        visibleCommands.push(contextItems[ci]);
                        shownNames[contextItems[ci].name] = true;
                    }
                }
            }

            // All commands.
            var remaining = allCommands.filter(function(c) { return !shownNames[c.name]; });
            remaining.sort(function(a, b) { return a.name.localeCompare(b.name); });
            if (remaining.length > 0) {
                visibleCommands.push({_section: 'All Commands'});
                for (var ai = 0; ai < remaining.length; ai++) {
                    visibleCommands.push(remaining[ai]);
                }
            }
        } else {
            var scored = [];
            for (var i = 0; i < allCommands.length; i++) {
                var s = scoreCommand(allCommands[i], query);
                if (s > 0) scored.push({cmd: allCommands[i], score: s});
            }
            scored.sort(function(a, b) { return b.score - a.score; });
            visibleCommands = scored.map(function(item) { return item.cmd; });
        }
        selectedIdx = 0;
        while (selectedIdx < visibleCommands.length && visibleCommands[selectedIdx]._section) selectedIdx++;
        renderPaletteResults();
    }

    function renderPaletteResults() {
        if (!resultsDiv) return;

        if (pendingCommand) {
            var options = pendingCommand.argType ? getOptionsForType(pendingCommand.argType) : [];
            var argFields = parseArgsTemplate(pendingCommand.args);

            var formHtml = '<div class="command-args-prompt">' +
                '<div class="command-args-header">gc ' + escapeHtml(pendingCommand.name) + '</div>';

            for (var i = 0; i < argFields.length; i++) {
                var field = argFields[i];
                var fieldId = 'arg-field-' + i;
                var isFirstField = (i === 0) && !field.flag;
                var hasOptions = isFirstField && pendingCommand.argType && options.length > 0;
                var noOptions = isFirstField && pendingCommand.argType && options.length === 0;
                var isMessageField = field.name === 'message' || field.name === 'body';

                formHtml += '<div class="command-field">';
                formHtml += '<label class="command-field-label" for="' + fieldId + '">' + escapeHtml(field.name) + '</label>';

                if (hasOptions) {
                    formHtml += '<select id="' + fieldId + '" class="command-field-select" data-flag="' + (field.flag || '') + '">';
                    formHtml += '<option value="">Select ' + escapeHtml(field.name) + '...</option>';
                    for (var j = 0; j < options.length; j++) {
                        var opt = options[j];
                        var disabledAttr = opt.disabled ? ' disabled' : '';
                        formHtml += '<option value="' + escapeHtml(opt.value) + '"' + disabledAttr + '>' + escapeHtml(opt.label) + '</option>';
                    }
                    formHtml += '</select>';
                } else if (noOptions) {
                    formHtml += '<input type="text" id="' + fieldId + '" class="command-field-input" data-flag="' + (field.flag || '') + '" placeholder="No ' + escapeHtml(pendingCommand.argType) + ' available">';
                } else if (isMessageField) {
                    formHtml += '<textarea id="' + fieldId + '" class="command-field-textarea" data-flag="' + (field.flag || '') + '" placeholder="Enter ' + escapeHtml(field.name) + '..." rows="3"></textarea>';
                } else {
                    formHtml += '<input type="text" id="' + fieldId + '" class="command-field-input" data-flag="' + (field.flag || '') + '" placeholder="Enter ' + escapeHtml(field.name) + '...">';
                }
                formHtml += '</div>';
            }

            if (argFields.length === 0 && pendingCommand.args) {
                formHtml += '<div class="command-field">';
                formHtml += '<input type="text" id="arg-field-0" class="command-field-input" placeholder="' + escapeHtml(pendingCommand.args) + '">';
                formHtml += '</div>';
            }

            formHtml += '<div class="command-args-actions">' +
                '<button id="command-args-run" class="command-args-btn run">Run</button>' +
                '<button id="command-args-cancel" class="command-args-btn cancel">Cancel</button>' +
                '</div></div>';

            resultsDiv.innerHTML = formHtml;

            var firstField = resultsDiv.querySelector('#arg-field-0');
            if (firstField) firstField.focus();

            var runBtn = document.getElementById('command-args-run');
            var cancelBtn = document.getElementById('command-args-cancel');

            if (runBtn) {
                runBtn.onclick = function() {
                    runBtn.classList.add('loading');
                    runBtn.textContent = 'Running';
                    runWithArgsFromForm(argFields.length || 1);
                };
            }
            if (cancelBtn) {
                cancelBtn.onclick = function() {
                    pendingCommand = null;
                    filterCommands(searchInput ? searchInput.value : '');
                };
            }

            resultsDiv.querySelectorAll('input, select').forEach(function(el) {
                el.onkeydown = function(ev) {
                    if (ev.key === 'Enter') {
                        ev.preventDefault();
                        runWithArgsFromForm(argFields.length || 1);
                    } else if (ev.key === 'Escape') {
                        ev.preventDefault();
                        pendingCommand = null;
                        filterCommands(searchInput ? searchInput.value : '');
                    }
                };
            });
            return;
        }

        if (visibleCommands.length === 0) {
            resultsDiv.innerHTML = '<div class="command-palette-empty">No matching commands</div>';
            return;
        }

        var currentQuery = searchInput ? searchInput.value.trim() : '';
        var html = '';

        if (currentQuery) {
            for (var i2 = 0; i2 < visibleCommands.length; i2++) {
                var cmd = visibleCommands[i2];
                var cls = 'command-item' + (i2 === selectedIdx ? ' selected' : '');
                var argsHint = cmd.args ? ' <span class="command-args">' + escapeHtml(cmd.args) + '</span>' : '';
                var nameHtml = highlightMatch('gc ' + cmd.name, currentQuery);
                html += '<div class="' + cls + '" data-cmd-name="' + escapeHtml(cmd.name) + '" data-cmd-args="' + escapeHtml(cmd.args || '') + '">' +
                    '<span class="command-name">' + nameHtml + argsHint + '</span>' +
                    '<span class="command-desc">' + escapeHtml(cmd.desc || '') + '</span>' +
                    '<span class="command-category">' + escapeHtml(cmd.category || '') + '</span>' +
                    '</div>';
            }
        } else {
            for (var j2 = 0; j2 < visibleCommands.length; j2++) {
                var item = visibleCommands[j2];
                if (item._section) {
                    html += '<div class="command-section-header">' + escapeHtml(item._section) + '</div>';
                    continue;
                }
                var cls2 = 'command-item' + (j2 === selectedIdx ? ' selected' : '');
                var argsHint2 = item.args ? ' <span class="command-args">' + escapeHtml(item.args) + '</span>' : '';
                var icon2 = item._recent ? '<span class="command-recent-icon">\u21BB</span>' : '';
                html += '<div class="' + cls2 + '" data-cmd-name="' + escapeHtml(item.name) + '" data-cmd-args="' + escapeHtml(item.args || '') + '">' +
                    icon2 +
                    '<span class="command-name">gc ' + escapeHtml(item.name) + argsHint2 + '</span>' +
                    '<span class="command-desc">' + escapeHtml(item.desc || '') + '</span>' +
                    '<span class="command-category">' + escapeHtml(item.category || '') + '</span>' +
                    '</div>';
            }
        }
        resultsDiv.innerHTML = html;

        var selectedEl = resultsDiv.querySelector('.command-item.selected');
        if (selectedEl) selectedEl.scrollIntoView({block: 'nearest'});
    }

    function runWithArgsFromForm(fieldCount) {
        var args = [];
        for (var i = 0; i < fieldCount; i++) {
            var field = document.getElementById('arg-field-' + i);
            if (field) {
                var val = field.value.trim();
                var flag = field.getAttribute('data-flag');
                if (val) {
                    if (flag) {
                        args.push(flag);
                        args.push('"' + val.replace(/"/g, '\\"') + '"');
                    } else {
                        args.push(val);
                    }
                }
            }
        }
        if (pendingCommand) {
            var fullCmd = pendingCommand.name + (args.length ? ' ' + args.join(' ') : '');
            pendingCommand = null;
            runCommand(fullCmd);
        }
    }

    function openPalette() {
        isPaletteOpen = true;
        pendingCommand = null;
        if (overlay) {
            overlay.style.display = 'flex';
            overlay.classList.add('open');
        }
        if (searchInput) {
            searchInput.value = '';
            searchInput.focus();
        }
        filterCommands('');
        fetchOptions();
    }

    function closePalette() {
        isPaletteOpen = false;
        pendingCommand = null;
        if (overlay) {
            overlay.classList.remove('open');
            overlay.style.display = 'none';
        }
        if (searchInput) searchInput.value = '';
        visibleCommands = [];
        if (resultsDiv) resultsDiv.innerHTML = '';
    }

    function selectCommand(cmdName, cmdArgs) {
        if (cmdArgs) {
            var cmd = allCommands.find(function(c) { return c.name === cmdName; });
            if (cmd) {
                pendingCommand = cmd;
                if (cmd.argType && !cachedOptions) {
                    fetchOptions().then(function() { renderPaletteResults(); });
                } else {
                    renderPaletteResults();
                }
                return;
            }
        }
        runCommand(cmdName);
    }

    function runCommand(cmdName) {
        if (executionLock || !cmdName) return;

        closePalette();

        var baseName = cmdName.split(' ').slice(0, 3).join(' ');
        var matchedCmd = allCommands.find(function(c) { return cmdName.indexOf(c.name) === 0; });
        saveRecentCommand(matchedCmd ? matchedCmd.name : baseName);

        executionLock = true;
        showToast('info', 'Running...', 'gc ' + cmdName);

        dispatchCommandAsWSAction(cmdName)
            .then(function(data) {
                showToast('success', 'Success', 'gc ' + cmdName);
                if (data && typeof data === 'object') {
                    showOutput(cmdName, JSON.stringify(data, null, 2));
                } else if (data && typeof data === 'string' && data.trim()) {
                    showOutput(cmdName, data);
                }
            })
            .catch(function(err) {
                showToast('error', 'Error', err.message || 'Request failed');
            })
            .finally(function() {
                setTimeout(function() { executionLock = false; }, 1000);
            });
    }

    // Command palette click handler.
    if (resultsDiv) {
        resultsDiv.addEventListener('click', function(e) {
            var item = e.target.closest('.command-item');
            if (!item) return;
            e.preventDefault();
            e.stopPropagation();
            var cmdName = item.getAttribute('data-cmd-name');
            var cmdArgs = item.getAttribute('data-cmd-args');
            if (cmdName) selectCommand(cmdName, cmdArgs);
        });
    }

    // Open palette button.
    document.addEventListener('click', function(e) {
        if (e.target.closest('#open-palette-btn')) {
            e.preventDefault();
            openPalette();
            return;
        }
        if (e.target === overlay) {
            closePalette();
        }
    });

    // Keyboard handling.
    document.addEventListener('keydown', function(e) {
        // Cmd+K / Ctrl+K toggles palette.
        if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
            e.preventDefault();
            if (isPaletteOpen) closePalette(); else openPalette();
            return;
        }

        // Escape closes expanded panels when palette is not open.
        if (!isPaletteOpen && e.key === 'Escape') {
            var expanded = document.querySelector('.panel.expanded');
            if (expanded) {
                e.preventDefault();
                expanded.classList.remove('expanded');
                var expandBtn = expanded.querySelector('.expand-btn');
                if (expandBtn) expandBtn.textContent = 'Expand';
                window.pauseRefresh = false;
                return;
            }
        }

        if (!isPaletteOpen) return;
        if (pendingCommand) return;

        if (e.key === 'Escape') {
            e.preventDefault();
            closePalette();
            return;
        }

        if (e.key === 'ArrowDown') {
            e.preventDefault();
            if (visibleCommands.length > 0) {
                var next = selectedIdx + 1;
                while (next < visibleCommands.length && visibleCommands[next]._section) next++;
                if (next < visibleCommands.length) selectedIdx = next;
                renderPaletteResults();
            }
            return;
        }

        if (e.key === 'ArrowUp') {
            e.preventDefault();
            var prev = selectedIdx - 1;
            while (prev >= 0 && visibleCommands[prev]._section) prev--;
            if (prev >= 0) selectedIdx = prev;
            renderPaletteResults();
            return;
        }

        if (e.key === 'Enter') {
            e.preventDefault();
            var selected = visibleCommands[selectedIdx];
            if (selected && !selected._section) {
                selectCommand(selected.name, selected.args);
            }
            return;
        }
    });

    // Input filtering.
    if (searchInput) {
        searchInput.addEventListener('input', function() {
            filterCommands(searchInput.value);
        });
    }

    // ================================================================
    // Section 7: Event Subscription + Refresh
    // ================================================================

    function _subscribeEvents() {
        var payload = {kind: 'events'};
        if (_lastEventCursor) {
            if (_selectedCity) {
                payload.after_seq = parseInt(_lastEventCursor, 10) || 0;
            } else {
                payload.after_cursor = _lastEventCursor;
            }
        }
        wsRequest('subscription.start', payload)
            .then(function() { _subscriptionRetry = 0; })
            .catch(function(err) {
                handleError(err, 'subscription.start');
                if (_subscriptionRetry++ < 3) {
                    var delay = Math.min(1000 * Math.pow(2, _subscriptionRetry), 10000);
                    setTimeout(_subscribeEvents, delay);
                }
            });
    }

    function _handleWSEvent(msg) {
        // Track cursor for reconnect resume.
        if (msg.cursor) _lastEventCursor = msg.cursor;
        else if (msg.index) _lastEventCursor = String(msg.index);

        if (window.pauseRefresh) return;
        var eventType = msg.event_type || '';

        _scheduleActivityRefresh();
        if (eventType && _observationTypes[eventType]) return;
        _scheduleFullRefresh();
    }

    function _scheduleActivityRefresh() {
        if (_activityTimer) return;
        _activityTimer = setTimeout(function() {
            _activityTimer = null;
            if (window.pauseRefresh) return;
            wsRequest('events.list', {limit: 50}).then(function(data) {
                renderActivityPanel(data.items || []);
            }).catch(function(err) { handleError(err, 'activity.refresh'); });
        }, _activityThrottle);
    }

    function _scheduleFullRefresh() {
        if (_fullRefreshTimer) clearTimeout(_fullRefreshTimer);
        _fullRefreshTimer = setTimeout(function() {
            _fullRefreshTimer = null;
            if (_activityTimer) { clearTimeout(_activityTimer); _activityTimer = null; }
            if (window.pauseRefresh) return;
            loadDashboard();
        }, 500);
    }

    // ================================================================
    // Section 8: Expand/Collapse + Initialization
    // ================================================================

    // Beads panel tab and rig filter state.
    var currentWorkTab = 'ready';
    var currentRigFilter = 'all';

    function applyBeadsFilter() {
        var rows = document.querySelectorAll('#work-table tbody tr');
        var visibleCount = 0;
        rows.forEach(function(row) {
            var status = row.getAttribute('data-status') || 'ready';
            var rig = row.getAttribute('data-rig') || '';
            var tabMatch = currentWorkTab === 'all' ||
                (currentWorkTab === 'ready' && status === 'ready') ||
                (currentWorkTab === 'progress' && status === 'progress');
            var rigMatch = currentRigFilter === 'all' || rig === currentRigFilter;
            if (tabMatch && rigMatch) {
                row.style.display = '';
                visibleCount++;
            } else {
                row.style.display = 'none';
            }
        });
        var countEl = document.querySelector('#beads-panel .panel-header .count');
        if (countEl) countEl.textContent = visibleCount;

        // Toggle empty state.
        var emptyEl = document.getElementById('issues-empty');
        if (emptyEl) emptyEl.style.display = visibleCount === 0 ? 'block' : 'none';
    }

    function switchWorkTab(tab) {
        currentWorkTab = tab;
        document.querySelectorAll('.panel-tabs .tab-btn').forEach(function(btn) {
            btn.classList.remove('active');
            if (btn.getAttribute('data-tab') === tab) btn.classList.add('active');
        });
        applyBeadsFilter();
    }
    window.switchWorkTab = switchWorkTab;

    function switchRigFilter(rig) {
        currentRigFilter = rig;
        document.querySelectorAll('.rig-filter-tabs .rig-btn').forEach(function(btn) {
            btn.classList.remove('active');
            if (btn.getAttribute('data-rig') === rig) btn.classList.add('active');
        });
        applyBeadsFilter();
    }
    window.switchRigFilter = switchRigFilter;

    // Expand button handler.
    document.addEventListener('click', function(e) {
        var btn = e.target.closest('.expand-btn');
        if (!btn) return;

        e.preventDefault();
        var panel = btn.closest('.panel');
        if (!panel) return;

        if (panel.classList.contains('expanded')) {
            panel.classList.remove('expanded');
            btn.textContent = 'Expand';
            window.pauseRefresh = false;
        } else {
            document.querySelectorAll('.panel.expanded').forEach(function(p) {
                p.classList.remove('expanded');
                var b = p.querySelector('.expand-btn');
                if (b) b.textContent = 'Expand';
            });
            panel.classList.add('expanded');
            btn.textContent = '\u2715 Close';
            window.pauseRefresh = true;
        }
    });

    // Collapse button handler.
    document.addEventListener('click', function(e) {
        var btn = e.target.closest('.collapse-btn');
        if (!btn) return;
        e.preventDefault();
        var panel = btn.closest('.panel');
        if (!panel) return;
        panel.classList.toggle('collapsed');
    });

    // Activity filter handlers.
    document.addEventListener('click', function(e) {
        var btn = e.target.closest('.tl-filter-btn');
        if (!btn) return;
        e.preventDefault();
        var filterType = btn.getAttribute('data-filter');
        var value = btn.getAttribute('data-value');

        if (filterType === 'category') {
            _activityCategoryFilter = value;
            document.querySelectorAll('.tl-filter-btn[data-filter="category"]').forEach(function(b) {
                b.classList.remove('active');
            });
            btn.classList.add('active');
        }
        applyActivityFilters();
    });

    on('tl-rig-filter', 'change', function(e) {
        _activityRigFilter = e.target.value;
        applyActivityFilters();
    });

    on('tl-agent-filter', 'change', function(e) {
        _activityAgentFilter = e.target.value;
        applyActivityFilters();
    });

    // Initialize: connect WebSocket (which triggers hello -> loadDashboard).
    connectWebSocket();

})();
