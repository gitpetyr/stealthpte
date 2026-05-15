/// Adapter wrapping a split WebSocket stream as a tokio AsyncRead+AsyncWrite,
/// so yamux can use it as its underlying transport.
use futures_util::{SinkExt, StreamExt};
use std::io;
use std::pin::Pin;
use std::task::{Context, Poll};
use tokio::io::{AsyncRead, AsyncWrite, ReadBuf};
use tokio_tungstenite::tungstenite::Message;
use tokio_tungstenite::WebSocketStream;
use tokio::net::TcpStream;

type WsSink = futures_util::stream::SplitSink<
    tokio_tungstenite::MaybeTlsStream<TcpStream>,
    Message,
>;
type WsSource = futures_util::stream::SplitStream<
    tokio_tungstenite::MaybeTlsStream<TcpStream>,
>;

// Use type alias to avoid generic complexity
pub use inner::WsConn;

mod inner {
    use super::*;
    use bytes::Bytes;

    pub struct WsConn {
        sink: WsSink,
        source: WsSource,
        read_buf: Bytes,
    }

    impl WsConn {
        pub fn new(sink: WsSink, source: WsSource) -> Self {
            Self { sink, source, read_buf: Bytes::new() }
        }
    }

    impl AsyncRead for WsConn {
        fn poll_read(
            mut self: Pin<&mut Self>,
            cx: &mut Context<'_>,
            buf: &mut ReadBuf<'_>,
        ) -> Poll<io::Result<()>> {
            loop {
                if !self.read_buf.is_empty() {
                    let n = buf.remaining().min(self.read_buf.len());
                    buf.put_slice(&self.read_buf[..n]);
                    self.read_buf = self.read_buf.slice(n..);
                    return Poll::Ready(Ok(()));
                }
                match Pin::new(&mut self.source).poll_next(cx) {
                    Poll::Pending => return Poll::Pending,
                    Poll::Ready(None) => return Poll::Ready(Ok(())),
                    Poll::Ready(Some(Err(e))) => {
                        return Poll::Ready(Err(io::Error::new(io::ErrorKind::Other, e)))
                    }
                    Poll::Ready(Some(Ok(msg))) => match msg {
                        Message::Binary(data) => {
                            self.read_buf = Bytes::from(data);
                        }
                        Message::Close(_) => return Poll::Ready(Ok(())),
                        _ => continue,
                    },
                }
            }
        }
    }

    impl AsyncWrite for WsConn {
        fn poll_write(
            mut self: Pin<&mut Self>,
            cx: &mut Context<'_>,
            buf: &[u8],
        ) -> Poll<io::Result<usize>> {
            match Pin::new(&mut self.sink).poll_ready(cx) {
                Poll::Pending => return Poll::Pending,
                Poll::Ready(Err(e)) => {
                    return Poll::Ready(Err(io::Error::new(io::ErrorKind::Other, e)))
                }
                Poll::Ready(Ok(())) => {}
            }
            let msg = Message::Binary(buf.to_vec().into());
            match Pin::new(&mut self.sink).start_send(msg) {
                Ok(()) => Poll::Ready(Ok(buf.len())),
                Err(e) => Poll::Ready(Err(io::Error::new(io::ErrorKind::Other, e))),
            }
        }

        fn poll_flush(
            mut self: Pin<&mut Self>,
            cx: &mut Context<'_>,
        ) -> Poll<io::Result<()>> {
            Pin::new(&mut self.sink)
                .poll_flush(cx)
                .map_err(|e| io::Error::new(io::ErrorKind::Other, e))
        }

        fn poll_shutdown(
            mut self: Pin<&mut Self>,
            cx: &mut Context<'_>,
        ) -> Poll<io::Result<()>> {
            Pin::new(&mut self.sink)
                .poll_close(cx)
                .map_err(|e| io::Error::new(io::ErrorKind::Other, e))
        }
    }
}
