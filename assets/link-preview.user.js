// ==UserScript==
// @name         Glance Link Preview
// @namespace    https://feed.tiulp.in/
// @version      1.0
// @description  Show link previews on hover for Glance dashboard
// @author       You
// @match        http://localhost:8080/*
// @match        https://feed.tiulp.in/*
// @grant        none
// ==/UserScript==

(function() {
    'use strict';
    
    var PREVIEW_URL = 'http://localhost:5050/preview?url=';
    var cache = {};
    var previewEl = null;
    var hideTimeout = null;
    var currentLink = null;
    
    function createPreviewElement() {
        var el = document.createElement('div');
        el.id = 'link-preview-tooltip';
        el.style.cssText = `
            position: fixed;
            z-index: 10000;
            width: 380px;
            max-width: 90vw;
            background: hsl(var(--color-widget-background, 220 20% 14%));
            border: 1px solid hsl(var(--color-text-subdue, 220 10% 40%) / 0.3);
            border-radius: 12px;
            box-shadow: 0 20px 50px rgba(0,0,0,0.5);
            padding: 0;
            display: none;
            overflow: hidden;
            font-family: var(--font, system-ui, sans-serif);
        `;
        document.body.appendChild(el);
        return el;
    }
    
    function positionPreview(e) {
        if (!previewEl) return;
        var rect = previewEl.getBoundingClientRect();
        var x = e.clientX + 15;
        var y = e.clientY + 15;
        
        if (x + rect.width > window.innerWidth - 20) {
            x = e.clientX - rect.width - 15;
        }
        if (y + rect.height > window.innerHeight - 20) {
            y = window.innerHeight - rect.height - 20;
        }
        if (y < 10) y = 10;
        
        previewEl.style.left = x + 'px';
        previewEl.style.top = y + 'px';
    }
    
    function fetchPreview(url) {
        if (cache[url]) return Promise.resolve(cache[url]);
        
        return fetch(PREVIEW_URL + encodeURIComponent(url))
            .then(function(r) { return r.json(); })
            .then(function(data) { 
                cache[url] = data; 
                return data; 
            })
            .catch(function(e) { 
                return { error: e.message }; 
            });
    }
    
    function renderPreview(data) {
        if (data.error) {
            return '<div style="padding:20px;text-align:center;color:hsl(var(--color-text-subdue, 220 10% 50%));">Could not load preview</div>';
        }
        
        var img = data.image 
            ? '<img src="' + data.image + '" style="width:100%;height:180px;object-fit:cover;display:block;" onerror="this.style.display=\'none\'">' 
            : '';
        
        return img + 
            '<div style="padding:16px;">' +
                '<div style="font-size:15px;font-weight:600;color:hsl(var(--color-text-highlight, 220 20% 95%));line-height:1.4;margin-bottom:10px;">' + 
                    (data.title || 'No title') + 
                '</div>' +
                '<div style="font-size:13px;color:hsl(var(--color-text-base, 220 15% 75%));opacity:0.85;line-height:1.5;display:-webkit-box;-webkit-line-clamp:3;-webkit-box-orient:vertical;overflow:hidden;">' + 
                    (data.description || '') + 
                '</div>' +
                '<div style="font-size:12px;color:hsl(var(--color-text-subdue, 220 10% 50%));margin-top:12px;display:flex;align-items:center;gap:8px;">' +
                    '<img src="' + (data.favicon || '') + '" style="width:16px;height:16px;border-radius:3px;" onerror="this.style.display=\'none\'">' +
                    '<span>' + (data.domain || '') + '</span>' +
                '</div>' +
            '</div>';
    }
    
    function showPreview(link, e) {
        if (!previewEl) previewEl = createPreviewElement();
        clearTimeout(hideTimeout);
        currentLink = link;
        
        previewEl.innerHTML = '<div style="padding:30px;text-align:center;color:hsl(var(--color-text-subdue, 220 10% 50%));">Loading...</div>';
        previewEl.style.display = 'block';
        positionPreview(e);
        
        fetchPreview(link.href).then(function(data) {
            if (currentLink === link) {
                previewEl.innerHTML = renderPreview(data);
                positionPreview(e);
            }
        });
    }
    
    function hidePreview() {
        hideTimeout = setTimeout(function() {
            if (previewEl) previewEl.style.display = 'none';
            currentLink = null;
        }, 200);
    }
    
    function isExternalLink(link) {
        if (!link.href || link.href.indexOf('javascript:') === 0) return false;
        try {
            var u = new URL(link.href);
            return u.origin !== window.location.origin;
        } catch(e) {
            return false;
        }
    }
    
    // Event listeners
    document.addEventListener('mouseover', function(e) {
        var link = e.target.closest('a');
        if (link && isExternalLink(link)) {
            showPreview(link, e);
        }
    });
    
    document.addEventListener('mouseout', function(e) {
        var link = e.target.closest('a');
        if (link) hidePreview();
    });
    
    document.addEventListener('mousemove', function(e) {
        if (currentLink && previewEl && previewEl.style.display === 'block') {
            positionPreview(e);
        }
    });
    
    // Keep preview visible when hovering over it
    document.addEventListener('mouseover', function(e) {
        if (e.target.closest('#link-preview-tooltip')) {
            clearTimeout(hideTimeout);
        }
    });
    
    document.addEventListener('mouseout', function(e) {
        if (e.target.closest('#link-preview-tooltip')) {
            hidePreview();
        }
    });
    
    console.log('✅ Glance Link Preview loaded');
})();

