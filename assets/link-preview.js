(function() {
  var PREVIEW_URL = '/api/preview?url=';
  var PREVIEWS_URL = '/api/previews';
  var PROXY_URL = '/api/proxy-image?url=';
  var cache = {};
  var previewEl = null;
  var hideTimeout = null;
  var showTimeout = null;
  var currentLink = null;
  var preloadedUrls = new Set();
  
  var PREVIEW_WIDTH = 360;
  var PREVIEW_MARGIN = 16;
  var PRELOAD_BATCH_SIZE = 15;
  var PRELOAD_DELAY = 1000;
  
  var SKIP_DOMAINS = ['reddit.com', 'lobste.rs', 'news.ycombinator.com'];
  
  function isExternalLink(link) {
    if (!link.href || link.href.indexOf('javascript:') === 0) return false;
    if (link.href.indexOf('#') === 0) return false;
    try { 
      var u = new URL(link.href);
      for (var i = 0; i < SKIP_DOMAINS.length; i++) {
        if (u.hostname.includes(SKIP_DOMAINS[i])) return false;
      }
      return u.origin !== window.location.origin; 
    } catch(e) { 
      return false; 
    }
  }

  function preloadPreviews() {
    var links = document.querySelectorAll('a[href]');
    var urls = [];
    
    for (var i = 0; i < links.length && urls.length < PRELOAD_BATCH_SIZE * 3; i++) {
      var link = links[i];
      if (isExternalLink(link) && !preloadedUrls.has(link.href) && !cache[link.href]) {
        urls.push(link.href);
        preloadedUrls.add(link.href);
      }
    }
    
    if (urls.length === 0) return;
    
    for (var batch = 0; batch < urls.length; batch += PRELOAD_BATCH_SIZE) {
      var batchUrls = urls.slice(batch, batch + PRELOAD_BATCH_SIZE);
      var params = batchUrls.map(function(u) { return 'url=' + encodeURIComponent(u); }).join('&');
      
      fetch(PREVIEWS_URL + '?' + params)
        .then(function(r) { return r.json(); })
        .then(function(results) {
          results.forEach(function(data) {
            if (data.url) cache[data.url] = data;
          });
        })
        .catch(function() {});
    }
  }
  
  setTimeout(preloadPreviews, PRELOAD_DELAY);
  
  function createPreviewElement() {
    var el = document.createElement('div');
    el.id = 'link-preview-tooltip';
    el.style.cssText = [
      'position: fixed',
      'z-index: 10000',
      'width: ' + PREVIEW_WIDTH + 'px',
      'max-width: calc(100vw - 32px)',
      'background: #121212',
      'border: 1px solid #2a2a2a',
      'border-radius: 12px',
      'box-shadow: 0 20px 50px rgba(0,0,0,0.6)',
      'padding: 0',
      'display: none',
      'overflow: hidden',
      'font-family: var(--font, system-ui, -apple-system, sans-serif)',
      'opacity: 0',
      'transform: translateY(8px)',
      'transition: opacity 0.15s ease, transform 0.15s ease'
    ].join(';');
    document.body.appendChild(el);
    return el;
  }
  
  function positionPreview(link) {
    if (!previewEl || !link) return;
    
    var linkRect = link.getBoundingClientRect();
    var previewHeight = previewEl.offsetHeight || 300;
    var viewportWidth = window.innerWidth;
    var viewportHeight = window.innerHeight;
    
    var x, y;
    
    if (linkRect.right + PREVIEW_WIDTH + PREVIEW_MARGIN < viewportWidth) {
      x = linkRect.right + PREVIEW_MARGIN;
    } else if (linkRect.left - PREVIEW_WIDTH - PREVIEW_MARGIN > 0) {
      x = linkRect.left - PREVIEW_WIDTH - PREVIEW_MARGIN;
    } else {
      x = Math.max(PREVIEW_MARGIN, (viewportWidth - PREVIEW_WIDTH) / 2);
    }
    
    y = linkRect.top;
    
    if (y + previewHeight > viewportHeight - PREVIEW_MARGIN) {
      y = viewportHeight - previewHeight - PREVIEW_MARGIN;
    }
    if (y < PREVIEW_MARGIN) {
      y = PREVIEW_MARGIN;
    }
    
    previewEl.style.left = x + 'px';
    previewEl.style.top = y + 'px';
    previewEl.style.right = 'auto';
    previewEl.style.bottom = 'auto';
  }
  
  function showElement() {
    if (!previewEl) return;
    previewEl.style.display = 'block';
    previewEl.offsetHeight;
    previewEl.style.opacity = '1';
    previewEl.style.transform = 'translateY(0)';
  }
  
  function hideElement() {
    if (!previewEl) return;
    previewEl.style.opacity = '0';
    previewEl.style.transform = 'translateY(8px)';
    setTimeout(function() {
      if (previewEl && previewEl.style.opacity === '0') {
        previewEl.style.display = 'none';
      }
    }, 150);
  }
  
  function fetchPreview(url) {
    if (cache[url]) return Promise.resolve(cache[url]);
    return fetch(PREVIEW_URL + encodeURIComponent(url))
      .then(function(r) { return r.json(); })
      .then(function(data) { cache[url] = data; return data; })
      .catch(function(e) { return { error: e.message }; });
  }
  
  function escapeHtml(str) {
    if (!str) return '';
    return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  }
  
  function getProxiedImageUrl(imageUrl) {
    if (!imageUrl) return '';
    return PROXY_URL + encodeURIComponent(imageUrl);
  }
  
  function renderPreview(data) {
    if (data.error) {
      return '<div style="padding:24px;color:#666;text-align:center;font-size:13px;">Could not load preview</div>';
    }
    
    var imageHtml = '';
    if (data.image) {
      var proxiedImage = getProxiedImageUrl(data.image);
      imageHtml = '<div style="position:relative;width:100%;height:160px;background:#0a0a0a;overflow:hidden;">' +
        '<img src="' + escapeHtml(proxiedImage) + '" style="width:100%;height:100%;object-fit:cover;display:block;" onerror="this.parentElement.style.display=\'none\'">' +
        '</div>';
    }
    
    return imageHtml + 
      '<div style="padding:16px;">' +
        '<div style="font-size:14px;font-weight:600;color:#e8e8e8;line-height:1.4;margin-bottom:8px;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden;">' + 
          escapeHtml(data.title || 'No title') + 
        '</div>' +
        '<div style="font-size:12px;color:#888;line-height:1.5;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden;margin-bottom:12px;">' + 
          escapeHtml(data.description || '') + 
        '</div>' +
        '<div style="font-size:11px;color:#555;display:flex;align-items:center;gap:6px;">' +
          '<img src="' + escapeHtml(data.favicon || '') + '" style="width:14px;height:14px;border-radius:3px;background:#1a1a1a;" onerror="this.style.display=\'none\'">' + 
          '<span>' + escapeHtml(data.domain || '') + '</span>' +
        '</div>' +
      '</div>';
  }
  
  function renderLoading() {
    return '<div style="padding:32px;text-align:center;">' +
      '<div style="width:20px;height:20px;border:2px solid #333;border-top-color:#c46a6a;border-radius:50%;margin:0 auto 10px;animation:lp-spin 0.8s linear infinite;"></div>' +
      '<div style="color:#555;font-size:12px;">Loading...</div>' +
      '</div>' +
      '<style>@keyframes lp-spin{to{transform:rotate(360deg)}}</style>';
  }
  
  function showPreview(link) {
    if (!previewEl) previewEl = createPreviewElement();
    clearTimeout(hideTimeout);
    clearTimeout(showTimeout);
    
    showTimeout = setTimeout(function() {
      currentLink = link;
      
      if (cache[link.href]) {
        previewEl.innerHTML = renderPreview(cache[link.href]);
        positionPreview(link);
        showElement();
      } else {
        previewEl.innerHTML = renderLoading();
        positionPreview(link);
        showElement();
        
        fetchPreview(link.href).then(function(data) {
          if (currentLink === link) { 
            previewEl.innerHTML = renderPreview(data);
            setTimeout(function() { positionPreview(link); }, 50);
          }
        });
      }
    }, 150);
  }
  
  function hidePreview() {
    clearTimeout(showTimeout);
    hideTimeout = setTimeout(function() { 
      hideElement();
      currentLink = null; 
    }, 100);
  }
  
  document.addEventListener('mouseover', function(e) {
    var link = e.target.closest('a');
    if (link && isExternalLink(link)) showPreview(link);
  });
  
  document.addEventListener('mouseout', function(e) {
    var link = e.target.closest('a');
    if (link) hidePreview();
  });
  
  document.addEventListener('mouseover', function(e) {
    if (e.target.closest('#link-preview-tooltip')) {
      clearTimeout(hideTimeout);
      clearTimeout(showTimeout);
    }
  });
  
  document.addEventListener('mouseout', function(e) {
    if (e.target.closest('#link-preview-tooltip')) hidePreview();
  });
})();
