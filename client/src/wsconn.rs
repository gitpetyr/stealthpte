use futures_util::{Sink, Stream};
use std::io;
use std::pin::Pin;
use std::task::{Context, Poll};
use tokio::net::TcpStream;
use tokio_tungstenite::tungstenite::Message;
use tokio_tungstenite::WebSocketStream;

type WsSink = futures_util::stream::SplitSink<
    WebSocketStream<tokio_tungstenite::MaybeTlsStream<TcpStream>>,
    Message,
>;
type WsSource = futures_util::stream::SplitStream<
    WebSocketStream<tokio_tungstenite::MaybeTlsStream<TcpStream>>,
>;

pub use inner::WsConn;

mod inner {
    use super::*;
    use bytes::Bytes;
    use futures_util::io::{AsyncRead, AsyncWrite};

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
            self: Pin<&mut Self>,
            cx: &mut Context<'_>,
            buf: &mut [u8],
        ) -> Poll<io::Result<usize>> {
            let this = self.get_mut();
            loop {
                if !this.read_buf.is_empty() {
                    let n = buf.len().min(this.read_buf.len());
                    buf[..n].copy_from_slice(&this.read_buf[..n]);
                    this.read_buf = this.read_buf.slice(n..);
                    return Poll::Ready(Ok(n));
                }
                match Pin::new(&mut this.source).poll_next(cx) {
                    Poll::Pending => return Poll::Pending,
                    Poll::Ready(None) => return Poll::Ready(Ok(0)),
                    Poll::Ready(Some(Err(e))) => {
                        return Poll::Ready(Err(io::Error::new(io::ErrorKind::Other, e)))
                    }
                    Poll::Ready(Some(Ok(msg))) => match msg {
                        Message::Binary(data) => {
                            this.read_buf = Bytes::from(data);
                        }
                        Message::Close(_) => return Poll::Ready(Ok(0)),
                        _ => continue,
                    },
                }
            }
        }
    }

    impl AsyncWrite for WsConn {
        fn poll_write(
            self: Pin<&mut Self>,
            cx: &mut Context<'_>,
            buf: &[u8],
        ) -> Poll<io::Result<usize>> {
            let this = self.get_mut();
            match Pin::new(&mut this.sink).poll_ready(cx) {
                Poll::Pending => return Poll::Pending,
                Poll::Ready(Err(e)) => {
                    return Poll::Ready(Err(io::Error::new(io::ErrorKind::Other, e)))
                }
                Poll::Ready(Ok(())) => {}
            }
            let msg = Message::Binary(buf.to_vec().into());
            match Pin::new(&mut this.sink).start_send(msg) {
                Ok(()) => Poll::Ready(Ok(buf.len())),
                Err(e) => Poll::Ready(Err(io::Error::new(io::ErrorKind::Other, e))),
            }
        }

        fn poll_flush(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
            let this = self.get_mut();
            Pin::new(&mut this.sink)
                .poll_flush(cx)
                .map_err(|e| io::Error::new(io::ErrorKind::Other, e))
        }

        fn poll_close(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
            let this = self.get_mut();
            Pin::new(&mut this.sink)
                .poll_close(cx)
                .map_err(|e| io::Error::new(io::ErrorKind::Other, e))
        }
    }
}
