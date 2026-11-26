import { isValidElement, useState, type ReactNode } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { Check, Copy } from 'lucide-react';

interface MarkdownMessageProps {
  content: string;
}

function extractText(node: ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') {
    return String(node);
  }
  if (Array.isArray(node)) {
    return node.map(extractText).join('');
  }
  if (isValidElement<{ children?: ReactNode }>(node)) {
    return extractText(node.props.children);
  }
  return '';
}

function CodeBlock({ language, code }: { language: string; code: string }) {
  const [copied, setCopied] = useState(false);

  async function handleCopy() {
    try {
      await navigator.clipboard?.writeText(code);
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    } catch {
      // Clipboard API may be unavailable in restricted contexts.
    }
  }

  return (
    <div className="my-4 overflow-hidden rounded-xl border border-zinc-200/90 bg-[#111113] text-zinc-100 shadow-xs dark:border-zinc-800">
      <div className="flex items-center justify-between border-b border-zinc-800/90 bg-[#18181B] px-3.5 py-2">
        <span className="font-mono text-[11px] font-medium tracking-wide text-zinc-400">
          {language || 'code'}
        </span>
        <button
          type="button"
          onClick={() => void handleCopy()}
          className="inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-[11px] font-medium text-zinc-300 transition hover:bg-zinc-800 hover:text-white"
        >
          {copied ? (
            <>
              <Check className="h-3 w-3 text-emerald-400" />
              <span className="text-emerald-400">Copié</span>
            </>
          ) : (
            <>
              <Copy className="h-3 w-3" />
              <span>Copier</span>
            </>
          )}
        </button>
      </div>
      <pre className="overflow-x-auto p-4 font-mono text-[13px] leading-relaxed text-zinc-100">
        <code>{code}</code>
      </pre>
    </div>
  );
}

export function MarkdownMessage({ content }: MarkdownMessageProps) {
  return (
    <div className="min-w-0 space-y-3.5 text-[15px] leading-[1.7] text-zinc-800 dark:text-zinc-200">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          pre({ children }) {
            let language = '';
            let rawCode = '';
            if (isValidElement<{ className?: string; children?: ReactNode }>(children)) {
              const cls = children.props.className ?? '';
              const match = /language-([^\s]+)/.exec(cls);
              if (match) {
                language = match[1];
              }
              rawCode = extractText(children.props.children);
            } else {
              rawCode = extractText(children);
            }
            return <CodeBlock language={language} code={rawCode.replace(/\n$/, '')} />;
          },
          code({ children }) {
            return (
              <code className="rounded-md border border-zinc-200/80 bg-zinc-100/80 px-1.5 py-0.5 font-mono text-[13px] font-medium text-zinc-900 dark:border-zinc-800 dark:bg-zinc-800/80 dark:text-zinc-100">
                {children}
              </code>
            );
          },
          h1({ children }) {
            return (
              <h1 className="mt-6 mb-2.5 text-xl font-semibold tracking-tight text-zinc-950 first:mt-0 dark:text-zinc-50">
                {children}
              </h1>
            );
          },
          h2({ children }) {
            return (
              <h2 className="mt-5 mb-2 text-[17px] font-semibold tracking-tight text-zinc-950 first:mt-0 dark:text-zinc-50">
                {children}
              </h2>
            );
          },
          h3({ children }) {
            return (
              <h3 className="mt-4 mb-1.5 text-[15px] font-semibold text-zinc-950 first:mt-0 dark:text-zinc-100">
                {children}
              </h3>
            );
          },
          p({ children }) {
            return <p className="whitespace-pre-wrap break-words">{children}</p>;
          },
          ul({ children }) {
            return <ul className="list-disc space-y-1.5 pl-5 marker:text-zinc-400">{children}</ul>;
          },
          ol({ children }) {
            return (
              <ol className="list-decimal space-y-1.5 pl-5 marker:text-zinc-400">{children}</ol>
            );
          },
          li({ children }) {
            return <li className="break-words pl-0.5">{children}</li>;
          },
          blockquote({ children }) {
            return (
              <blockquote className="border-l-2 border-zinc-300 pl-4 text-zinc-600 italic dark:border-zinc-700 dark:text-zinc-400">
                {children}
              </blockquote>
            );
          },
          table({ children }) {
            return (
              <div className="my-4 overflow-x-auto rounded-xl border border-zinc-200/90 bg-white dark:border-zinc-800 dark:bg-zinc-900/40">
                <table className="w-full border-collapse text-left text-[13px]">{children}</table>
              </div>
            );
          },
          thead({ children }) {
            return (
              <thead className="border-b border-zinc-200 bg-zinc-50/90 dark:border-zinc-800 dark:bg-zinc-900/90">
                {children}
              </thead>
            );
          },
          th({ children }) {
            return (
              <th className="px-3.5 py-2.5 align-top text-xs font-semibold text-zinc-700 dark:text-zinc-200">
                {children}
              </th>
            );
          },
          tr({ children }) {
            return (
              <tr className="transition-colors hover:bg-zinc-50/70 dark:hover:bg-zinc-800/40">
                {children}
              </tr>
            );
          },
          td({ children }) {
            return (
              <td className="border-t border-zinc-200/70 px-3.5 py-2.5 align-top leading-relaxed text-zinc-700 dark:border-zinc-800/70 dark:text-zinc-300">
                {children}
              </td>
            );
          },
          a({ href, children }) {
            return (
              <a
                href={href}
                target="_blank"
                rel="noreferrer"
                className="font-medium text-indigo-600 underline decoration-indigo-500/35 underline-offset-2 hover:decoration-indigo-600 dark:text-indigo-400"
              >
                {children}
              </a>
            );
          },
          hr() {
            return <hr className="my-5 border-zinc-200/80 dark:border-zinc-800/80" />;
          },
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
