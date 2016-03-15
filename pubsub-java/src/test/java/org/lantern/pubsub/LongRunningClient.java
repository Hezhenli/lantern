package org.lantern.pubsub;

import java.util.Random;

public class LongRunningClient extends BaseClient {
    public static void main(String[] args) throws Exception {
        if (args.length != 1) {
            System.err.println("Please specify an authentication key");
            System.exit(1);
        }

        Random rand = new Random(System.currentTimeMillis());
        int id = Math.abs(rand.nextInt(100));
        Client client = newClient(args[0]);
        client.subscribe(TOPIC);
        for (int i = 0; i < Integer.MAX_VALUE; i++) {
            client.publish(TOPIC,
                    Client.utf8(String.format("%03d|%2$d", id, i)));
            Message msg = client.read();
            System.err.println(new String(msg.getBody()));
            Thread.sleep(2 * 60 * 1000);
        }
    }
}