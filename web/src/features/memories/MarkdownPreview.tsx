import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { Box, Paper } from '@mantine/core';

export interface MarkdownPreviewProps {
  content: string;
}

export function MarkdownPreview({ content }: MarkdownPreviewProps) {
  return (
    <Paper
      withBorder
      p="md"
      radius="sm"
      bg="var(--mantine-color-body)"
      style={{
        minHeight: 400,
        height: '100%',
        overflowY: 'auto',
      }}
    >
      <Box
        className="markdown-content"
        style={{
          lineHeight: 1.6,
          fontSize: '14px',
          wordBreak: 'break-word',
        }}
      >
        <ReactMarkdown remarkPlugins={[remarkGfm]}>
          {content || '*No content*'}
        </ReactMarkdown>
      </Box>
    </Paper>
  );
}
