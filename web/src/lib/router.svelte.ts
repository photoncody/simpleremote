// A tiny History API router. Routes are matched in App.svelte.

class Router {
  path = $state(location.pathname);
  search = $state(location.search);

  constructor() {
    addEventListener('popstate', () => this.sync());
  }

  private sync() {
    this.path = location.pathname;
    this.search = location.search;
  }

  navigate(to: string, replace = false) {
    if (to === location.pathname + location.search) return;
    if (replace) history.replaceState(null, '', to);
    else history.pushState(null, '', to);
    this.sync();
    scrollTo(0, 0);
  }

  param(name: string): string | null {
    return new URLSearchParams(this.search).get(name);
  }
}

export const router = new Router();

// Use on <a href="/tech/..."> to navigate without a page load.
export function link(node: HTMLAnchorElement) {
  const onClick = (e: MouseEvent) => {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const url = new URL(node.href);
    if (url.origin !== location.origin) return;
    e.preventDefault();
    router.navigate(url.pathname + url.search);
  };
  node.addEventListener('click', onClick);
  return { destroy: () => node.removeEventListener('click', onClick) };
}
